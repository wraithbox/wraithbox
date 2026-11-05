// Package synth is the synthetic address pool of one VM (S07-03-dns,
// "Synthetic addresses", "Synthetic pool"): which allowed name each
// address of 198.18.0.0/15 maps to, when an address is released, and
// which TCP connections to a synthetic address the stack accepts.
//
// The mapping decides which name a stream is for (S04-07-supervision,
// "The kept name mapping"). So the pool never gives an address to a
// second name before it has recorded the release with wb-hostd and
// reported it to wb-proxyd, and never while a connection to it is open
// (SEC05-default-deny). The pool holds at most the addresses of its
// prefix, and belongs to one VM (SEC13-bounded-resources,
// SEC08-proj-isolation).
package synth

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// The times of S07-03-dns, "Synthetic pool". The spec names the
// properties, and the values are here.
const (
	// TTL is the TTL of every A answer from the pool, and of the SOA
	// record of an allowed name's empty answers. X03-network-path used 60 s.
	TTL = 60 * time.Second
	// Guard is how long past its TTL an entry is kept, so that a guest
	// that holds an answer past its TTL still reaches the same name.
	Guard = 30 * time.Minute
	// SendTimeout bounds each accounting write and each name report or
	// release. A call that takes longer counts as failed
	// (S04-08-records-and-bounds, "Accounting records",
	// S07-05-stream-hand-off, "Transport").
	SendTimeout = 100 * time.Millisecond
	// SweepEvery is how often Run releases the entries that expired.
	SweepEvery = time.Minute
)

// MinPrefixBits is the shortest prefix a pool takes: 198.18.0.0/15 has
// 131,072 addresses, and the pool holds no more (SEC13-bounded-resources).
const MinPrefixBits = 15

// Entry is one mapping entry: a name and the synthetic address it maps
// to.
type Entry struct {
	Name dnsname.Name
	Addr netip.Addr
}

// LogAttrs returns the entry as log attributes. The name passed the name
// form rule, so it is printable ASCII.
func (e Entry) LogAttrs() []slog.Attr {
	return []slog.Attr{slog.String("name", e.Name.String()), slog.String("addr", e.Addr.String())}
}

// Deps are what a pool records with, reports to and decides streams
// with.
type Deps struct {
	// Accounting writes entries and releases to wb-hostd. Required.
	Accounting netd.Accounting
	// Reports sends name reports and releases to wb-proxyd. Required.
	Reports netd.NameReports
	// Ports decides the port of a stream. Nil allows no port.
	Ports netd.PortPolicy
	// HandOff takes accepted streams. Nil resets each stream on an
	// allowed port with handoff-unavailable.
	HandOff netd.StreamHandOff
	// Now is the monotonic clock entries expire by. Nil means time.Now.
	Now func() time.Time
	// Log gets failed writes and sends, releases and refused streams,
	// rate-limited per rule. Nil means slog.Default.
	Log *slog.Logger
}

// entry is a live entry.
type entry struct {
	Entry
	// answered is when the entry was last answered, or when the pool
	// started for a kept entry.
	answered time.Time
	// pins counts the open connections to the address (Open).
	pins int
}

// release is an address on its way back to the free list: recorded
// with wb-hostd once recorded is true, and then reported to wb-proxyd.
type release struct {
	Entry
	recorded bool
}

// Pool is one VM's synthetic pool. It is safe for concurrent use.
type Pool struct {
	prefix netip.Prefix
	size   uint32
	deps   Deps
	log    *slog.Logger

	// send orders the writes and sends: answers and sweeps hold it from
	// the first write to the last, so each release is recorded and
	// reported before any later entry or report (S07-06-name-mapping,
	// "Releases"). Open and DecideStream never take it.
	send sync.Mutex

	// mu guards the fields below. It is never held across a write or
	// send.
	mu      sync.Mutex
	byName  map[dnsname.Name]*entry
	byAddr  map[netip.Addr]*entry
	fresh   uint32              // offset of the next address never given out
	skip    map[netip.Addr]bool // kept addresses fresh must pass over
	free    []netip.Addr        // released addresses, first in, first out
	pending []release           // releases not yet recorded and reported, in order
}

// New returns the pool of prefix. It starts with the entries wb-hostd
// kept (S04-07-supervision, "A new wb-netd"), each counted as answered
// now. So none of their addresses goes to another name before its TTL
// plus Guard has passed and it is released. A kept entry outside the
// prefix, with a name that fails the name form rule, or with an address
// or a name another kept entry has refuses the start: wb-hostd checks the
// same entries, and this check is a second layer.
func New(prefix netip.Prefix, kept []Entry, deps Deps) (*Pool, error) {
	if !prefix.IsValid() || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.Bits() < MinPrefixBits {
		return nil, fmt.Errorf("synth: pool %s isn't an IPv4 network of at most /%d", prefix, MinPrefixBits)
	}
	if deps.Accounting == nil || deps.Reports == nil {
		return nil, errors.New("synth: a pool needs accounting and name reports")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	p := &Pool{
		prefix: prefix,
		size:   uint32(1) << (32 - prefix.Bits()),
		deps:   deps,
		log:    ratelog.New(deps.Log),
		byName: map[dnsname.Name]*entry{},
		byAddr: map[netip.Addr]*entry{},
		skip:   map[netip.Addr]bool{},
	}
	now := deps.Now()
	for i, k := range kept {
		if err := p.checkKept(k); err != nil {
			return nil, fmt.Errorf("synth: kept entry %d: %w", i, err)
		}
		e := &entry{Entry: k, answered: now}
		p.byName[k.Name] = e
		p.byAddr[k.Addr] = e
		p.skip[k.Addr] = true
	}
	return p, nil
}

// checkKept checks one kept entry against the prefix, the name form rule
// and the entries before it.
func (p *Pool) checkKept(k Entry) error {
	if !k.Addr.Is4() || !p.prefix.Contains(k.Addr) {
		return fmt.Errorf("address %s is outside %s", k.Addr, p.prefix)
	}
	n, r := dnsname.FromHost(k.Name.String())
	if r != nil {
		return r
	}
	if n != k.Name {
		return fmt.Errorf("name %q isn't in canonical form", k.Name)
	}
	if _, ok := p.byAddr[k.Addr]; ok {
		return fmt.Errorf("address %s is kept twice", k.Addr)
	}
	if _, ok := p.byName[k.Name]; ok {
		return fmt.Errorf("name %s is kept twice", k.Name)
	}
	return nil
}

// Answer gives name its address for an A answer, and returns it with an
// empty rule. In order, it:
//  1. sends the releases still pending;
//  2. finds the name's live entry, or takes a free address, after a sweep
//     when none is free;
//  3. writes the entry to wb-hostd;
//  4. reports it to wb-proxyd;
//  5. renews the entry's time.
//
// A failed step returns its rule, and the DNS server refuses the name:
// accounting-write-failed, handoff-send-failed, or synthetic-pool-full
// when no address is free (S07-03-dns, "Synthetic pool",
// S07-04-dns-refusals, "Order at arrival"). A new entry whose write or
// report failed isn't kept. Its address is released like an expired one,
// because wb-hostd or wb-proxyd may hold it.
func (p *Pool) Answer(ctx context.Context, name dnsname.Name) (netip.Addr, netd.Rule) {
	if name == (dnsname.Name{}) {
		return netip.Addr{}, dnsname.RuleForm
	}
	p.send.Lock()
	defer p.send.Unlock()
	if rule := p.flush(ctx); rule != "" {
		return netip.Addr{}, rule
	}
	p.mu.Lock()
	live := p.byName[name]
	p.mu.Unlock()
	if live != nil {
		if rule := p.record(ctx, live.Entry); rule != "" {
			return netip.Addr{}, rule
		}
		p.mu.Lock()
		live.answered = p.deps.Now()
		p.mu.Unlock()
		return live.Addr, ""
	}
	addr, rule := p.allocate(ctx)
	if rule != "" {
		return netip.Addr{}, rule
	}
	e := Entry{Name: name, Addr: addr}
	if rule := p.record(ctx, e); rule != "" {
		p.mu.Lock()
		p.pending = append(p.pending, release{Entry: e})
		p.mu.Unlock()
		return netip.Addr{}, rule
	}
	p.mu.Lock()
	n := &entry{Entry: e, answered: p.deps.Now()}
	p.byName[name] = n
	p.byAddr[addr] = n
	p.mu.Unlock()
	return addr, ""
}

// allocate takes a free address. When none is free it sweeps first. It
// is called with send held.
func (p *Pool) allocate(ctx context.Context) (netip.Addr, netd.Rule) {
	if a, ok := p.takeFree(); ok {
		return a, ""
	}
	if rule := p.sweep(ctx); rule != "" {
		return netip.Addr{}, rule
	}
	if a, ok := p.takeFree(); ok {
		return a, ""
	}
	p.log.LogAttrs(ctx, slog.LevelWarn, "netd synthetic pool full",
		slog.String("rule", string(netd.RuleSyntheticPoolFull)), slog.Int("live", p.Len()))
	return netip.Addr{}, netd.RuleSyntheticPoolFull
}

// takeFree takes the next address never given out, and then the address
// released longest ago, so a released address is reused as late as
// possible.
func (p *Pool) takeFree() (netip.Addr, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.fresh < p.size {
		a := p.addrAt(p.fresh)
		p.fresh++
		if p.skip[a] {
			delete(p.skip, a) // kept: it comes back through a release
			continue
		}
		return a, true
	}
	if len(p.free) == 0 {
		return netip.Addr{}, false
	}
	a := p.free[0]
	p.free = p.free[1:]
	return a, true
}

// addrAt is the address at an offset into the prefix.
func (p *Pool) addrAt(off uint32) netip.Addr {
	b := p.prefix.Addr().As4()
	binary.BigEndian.PutUint32(b[:], binary.BigEndian.Uint32(b[:])+off)
	return netip.AddrFrom4(b)
}

// record writes an entry to wb-hostd, then reports it to wb-proxyd
// (S04-07-supervision, "The kept name mapping", S07-06-name-mapping,
// "Name reports").
func (p *Pool) record(ctx context.Context, e Entry) netd.Rule {
	if err := p.within(ctx, func(c context.Context) error { return p.deps.Accounting.Entry(c, e.Name, e.Addr) }); err != nil {
		p.failed(ctx, netd.RuleAccountingWriteFailed, "entry", e, err)
		return netd.RuleAccountingWriteFailed
	}
	if err := p.within(ctx, func(c context.Context) error { return p.deps.Reports.Report(c, e.Name, e.Addr) }); err != nil {
		p.failed(ctx, netd.RuleHandoffSendFailed, "report", e, err)
		return netd.RuleHandoffSendFailed
	}
	return ""
}

// Sweep releases every entry whose TTL plus Guard has passed since its
// last answer and that has no open connection, in address order. It
// returns the rule of a release that failed, or "". The addresses whose
// release failed stay out of the pool until it is sent.
func (p *Pool) Sweep(ctx context.Context) netd.Rule {
	p.send.Lock()
	defer p.send.Unlock()
	return p.sweep(ctx)
}

// sweep is Sweep with send held.
func (p *Pool) sweep(ctx context.Context) netd.Rule {
	now := p.deps.Now()
	p.mu.Lock()
	var expired []Entry
	for _, e := range p.byAddr {
		if e.pins == 0 && now.Sub(e.answered) > TTL+Guard {
			expired = append(expired, e.Entry)
		}
	}
	slices.SortFunc(expired, func(a, b Entry) int { return a.Addr.Compare(b.Addr) })
	for _, e := range expired {
		delete(p.byName, e.Name)
		delete(p.byAddr, e.Addr)
		p.pending = append(p.pending, release{Entry: e})
	}
	p.mu.Unlock()
	return p.flush(ctx)
}

// flush records and reports the pending releases in order, and puts
// each address on the free list once both are done
// (S07-06-name-mapping, "Releases"). It stops at the first failure and
// returns its rule. The next flush starts again from that release. It is
// called with send held.
func (p *Pool) flush(ctx context.Context) netd.Rule {
	for {
		p.mu.Lock()
		if len(p.pending) == 0 {
			p.mu.Unlock()
			return ""
		}
		r := p.pending[0]
		p.mu.Unlock()
		if !r.recorded {
			if err := p.within(ctx, func(c context.Context) error { return p.deps.Accounting.Release(c, r.Addr) }); err != nil {
				p.failed(ctx, netd.RuleAccountingWriteFailed, "release", r.Entry, err)
				return netd.RuleAccountingWriteFailed
			}
			p.mu.Lock()
			p.pending[0].recorded = true
			p.mu.Unlock()
		}
		if err := p.within(ctx, func(c context.Context) error { return p.deps.Reports.Release(c, r.Addr) }); err != nil {
			p.failed(ctx, netd.RuleHandoffSendFailed, "release", r.Entry, err)
			return netd.RuleHandoffSendFailed
		}
		p.mu.Lock()
		p.pending = p.pending[1:]
		p.free = append(p.free, r.Addr)
		p.mu.Unlock()
		p.log.LogAttrs(ctx, slog.LevelInfo, "netd synthetic release", r.LogAttrs()...)
	}
}

// within runs one write or send under SendTimeout. A call that returns
// after the deadline failed, even without an error.
func (p *Pool) within(ctx context.Context, call func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	if err := call(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

// failed logs a failed write or send with its rule.
func (p *Pool) failed(ctx context.Context, rule netd.Rule, what string, e Entry, err error) {
	attrs := append([]slog.Attr{
		slog.String("rule", string(rule)),
		slog.String("message", what),
		slog.String("error", err.Error()),
	}, e.LogAttrs()...)
	p.log.LogAttrs(ctx, slog.LevelWarn, "netd synthetic "+what+" failed", attrs...)
}

// Run sweeps every SweepEvery until ctx is done.
func (p *Pool) Run(ctx context.Context) {
	tick := time.NewTicker(SweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.Sweep(ctx)
		}
	}
}

// Live returns the live entries in address order, the replay for a new
// hand-off socket (S07-06-name-mapping, "Replay").
func (p *Pool) Live() []Entry {
	p.mu.Lock()
	out := make([]Entry, 0, len(p.byAddr))
	for _, e := range p.byAddr {
		out = append(out, e.Entry)
	}
	p.mu.Unlock()
	slices.SortFunc(out, func(a, b Entry) int { return a.Addr.Compare(b.Addr) })
	return out
}

// Len returns the number of live entries.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byAddr)
}

// Open pins the live entry of addr until done runs, and returns its
// name. A pinned entry is never released. ok is false when addr has no
// live entry. done may run more than once, and only the first counts.
func (p *Pool) Open(addr netip.Addr) (name dnsname.Name, done func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.byAddr[addr]
	if e == nil {
		return dnsname.Name{}, nil, false
	}
	e.pins++
	var once sync.Once
	return e.Name, func() {
		once.Do(func() {
			p.mu.Lock()
			e.pins--
			p.mu.Unlock()
		})
	}, true
}

// lookup returns the name of addr's live entry.
func (p *Pool) lookup(addr netip.Addr) (dnsname.Name, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.byAddr[addr]
	if e == nil {
		return dnsname.Name{}, false
	}
	return e.Name, true
}
