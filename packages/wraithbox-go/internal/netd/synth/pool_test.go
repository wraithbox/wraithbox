package synth

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// clock is a monotonic clock the test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Unix(1_000_000, 0)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// expire is just past the time an entry is kept after its last answer.
const expire = TTL + Guard + time.Nanosecond

var errDown = errors.New("peer down")

// fixture is a pool with fakes and a fake clock.
type fixture struct {
	t       *testing.T
	p       *Pool
	calls   *nt.Calls
	clock   *clock
	ports   *nt.Ports
	handOff nt.HandOff
}

func newFixture(t *testing.T, prefix string, kept ...Entry) *fixture {
	t.Helper()
	f := &fixture{t: t, calls: &nt.Calls{}, clock: newClock(), ports: &nt.Ports{}, handOff: make(nt.HandOff, 4)}
	p, err := New(netip.MustParsePrefix(prefix), kept, Deps{
		Accounting: nt.Accounting{Calls: f.calls},
		Reports:    nt.NameReports{Calls: f.calls},
		Ports:      f.ports,
		HandOff:    f.handOff,
		Now:        f.clock.Now,
		Log:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.p = p
	return f
}

// answer answers a name and wants an address.
func (f *fixture) answer(host string) netip.Addr {
	f.t.Helper()
	a, rule := f.p.Answer(context.Background(), nt.Name(host))
	if rule != "" || !a.IsValid() {
		f.t.Fatalf("answer %s = %v, %q", host, a, rule)
	}
	return a
}

// refused answers a name and wants rule.
func (f *fixture) refused(host string, rule netd.Rule) {
	f.t.Helper()
	if a, got := f.p.Answer(context.Background(), nt.Name(host)); got != rule || a.IsValid() {
		f.t.Fatalf("answer %s = %v, %q, want %q", host, a, got, rule)
	}
}

// wantCalls checks the calls since the last check, and forgets them.
func (f *fixture) wantCalls(want ...nt.Call) {
	f.t.Helper()
	got := f.calls.List()
	f.calls.Reset()
	for i := range got {
		got[i].Err = nil
	}
	if !slices.Equal(got, want) {
		f.t.Fatalf("calls\n got %v\nwant %v", got, want)
	}
}

func entryCalls(host string, addr netip.Addr) []nt.Call {
	n := nt.Name(host)
	return []nt.Call{{Kind: nt.CallEntry, Name: n, Addr: addr}, {Kind: nt.CallReport, Name: n, Addr: addr}}
}

func releaseCalls(addr netip.Addr) []nt.Call {
	return []nt.Call{{Kind: nt.CallRelease, Addr: addr}, {Kind: nt.CallReportRelease, Addr: addr}}
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

// TestAnswerSameAddress checks that a live name keeps its address, and
// that each answer writes the entry and then reports it before it
// returns (S04-07-supervision, "The kept name mapping").
func TestAnswerSameAddress(t *testing.T) {
	f := newFixture(t, "198.18.0.0/15")
	a := f.answer("api.example.com")
	if a != addr("198.18.0.0") {
		t.Fatalf("first address %v", a)
	}
	f.wantCalls(entryCalls("api.example.com", a)...)
	f.clock.advance(TTL)
	if again := f.answer("api.example.com"); again != a {
		t.Fatalf("second answer %v, want %v", again, a)
	}
	f.wantCalls(entryCalls("api.example.com", a)...)
	if b := f.answer("cdn.example.com"); b == a {
		t.Fatalf("two names share %v", a)
	}
	if got := f.p.Live(); len(got) != 2 || got[0].Addr != a || got[0].Name != nt.Name("api.example.com") {
		t.Fatalf("live %v", got)
	}
}

// TestAnswerFails checks that a failed or slow write or report refuses
// the name with its rule, keeps no entry, and releases the address
// before it is given again, because the peer may hold it.
func TestAnswerFails(t *testing.T) {
	cases := []struct {
		name  string
		kind  nt.CallKind
		fault nt.Fault
		rule  netd.Rule
	}{
		{"entry fails", nt.CallEntry, nt.Fault{Err: errDown}, netd.RuleAccountingWriteFailed},
		{"entry slow", nt.CallEntry, nt.Fault{Delay: SendTimeout + 50*time.Millisecond}, netd.RuleAccountingWriteFailed},
		{"report fails", nt.CallReport, nt.Fault{Err: errDown}, netd.RuleHandoffSendFailed},
		{"report slow", nt.CallReport, nt.Fault{Delay: SendTimeout + 50*time.Millisecond}, netd.RuleHandoffSendFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, "198.18.0.0/15")
			f.calls.Fail(c.kind, c.fault)
			f.refused("api.example.com", c.rule)
			if n := f.p.Len(); n != 0 {
				t.Fatalf("%d live entries after a failed answer", n)
			}
			if _, _, ok := f.p.Open(addr("198.18.0.0")); ok {
				t.Fatal("the failed entry's address opens")
			}
			f.calls.Reset()
			f.calls.Fail(c.kind, nt.Fault{})
			b := f.answer("api.example.com")
			if b != addr("198.18.0.1") {
				t.Fatalf("answer after the failure %v, want the next fresh address", b)
			}
			f.wantCalls(append(releaseCalls(addr("198.18.0.0")), entryCalls("api.example.com", b)...)...)
		})
	}
}

// TestRenewFails checks that a failed renewal keeps the live entry with
// its old time.
func TestRenewFails(t *testing.T) {
	f := newFixture(t, "198.18.0.0/15")
	a := f.answer("api.example.com")
	f.clock.advance(Guard)
	f.calls.Fail(nt.CallEntry, nt.Fault{Err: errDown})
	f.refused("api.example.com", netd.RuleAccountingWriteFailed)
	if got := f.p.Live(); len(got) != 1 || got[0].Addr != a {
		t.Fatalf("a failed renewal left %v", got)
	}
	f.calls.Fail(nt.CallEntry, nt.Fault{})
	f.calls.Reset()
	f.clock.advance(expire - Guard)
	if rule := f.p.Sweep(context.Background()); rule != "" {
		t.Fatal(rule)
	}
	f.wantCalls(releaseCalls(a)...)
}

// TestRelease checks that an entry is released only after its TTL plus
// the guard since its last answer, with no open connection, and that the
// release is recorded and then reported.
func TestRelease(t *testing.T) {
	f := newFixture(t, "198.18.0.0/15")
	a := f.answer("api.example.com")
	b := f.answer("cdn.example.com")
	f.calls.Reset()

	f.clock.advance(TTL + Guard - time.Second)
	f.answer("cdn.example.com") // renewed
	_, done, ok := f.p.Open(a)
	if !ok {
		t.Fatal("open a live entry")
	}
	f.calls.Reset()
	f.clock.advance(time.Second)
	f.p.Sweep(context.Background())
	f.wantCalls() // exactly TTL plus guard has passed: kept

	f.clock.advance(time.Nanosecond)
	f.p.Sweep(context.Background())
	f.wantCalls() // a is pinned, b was renewed
	done()
	done() // a second call doesn't unpin again
	f.p.Sweep(context.Background())
	f.wantCalls(releaseCalls(a)...)
	if _, _, ok := f.p.Open(a); ok {
		t.Fatal("a released address opens")
	}

	f.clock.advance(expire)
	f.p.Sweep(context.Background())
	f.wantCalls(releaseCalls(b)...)
}

// TestReleaseRetried checks that a release that failed keeps the address
// out of the pool, and is sent again, from the step that failed, before
// any later report (S07-06-name-mapping, "Releases").
func TestReleaseRetried(t *testing.T) {
	f := newFixture(t, "198.18.0.0/31")
	a := f.answer("a.example.com")
	b := f.answer("b.example.com")
	f.clock.advance(expire)
	f.answer("b.example.com")
	f.calls.Reset()

	f.calls.Fail(nt.CallRelease, nt.Fault{Err: errDown})
	if rule := f.p.Sweep(context.Background()); rule != netd.RuleAccountingWriteFailed {
		t.Fatalf("sweep %q", rule)
	}
	f.calls.Reset()
	f.refused("c.example.com", netd.RuleAccountingWriteFailed)
	f.wantCalls(nt.Call{Kind: nt.CallRelease, Addr: a}) // no report went out

	f.calls.Fail(nt.CallRelease, nt.Fault{})
	f.calls.Fail(nt.CallReportRelease, nt.Fault{Err: errDown})
	f.refused("c.example.com", netd.RuleHandoffSendFailed)
	f.wantCalls(releaseCalls(a)...)
	f.refused("b.example.com", netd.RuleHandoffSendFailed) // a live name waits too
	f.wantCalls(nt.Call{Kind: nt.CallReportRelease, Addr: a})

	f.calls.Fail(nt.CallReportRelease, nt.Fault{})
	if c := f.answer("c.example.com"); c != a {
		t.Fatalf("c got %v, want the released %v", c, a)
	}
	f.wantCalls(append([]nt.Call{{Kind: nt.CallReportRelease, Addr: a}}, entryCalls("c.example.com", a)...)...)
	if got := f.answer("b.example.com"); got != b {
		t.Fatalf("b moved to %v", got)
	}
}

// TestPoolFull checks synthetic-pool-full, and that a full pool first
// frees the entries that expired.
func TestPoolFull(t *testing.T) {
	f := newFixture(t, "198.18.0.0/30")
	var addrs []netip.Addr
	for _, h := range []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"} {
		addrs = append(addrs, f.answer(h))
	}
	f.refused("e.example.com", netd.RuleSyntheticPoolFull)
	if n := f.p.Len(); n != 4 {
		t.Fatalf("%d live entries", n)
	}

	f.clock.advance(expire)
	for _, h := range []string{"a.example.com", "c.example.com", "d.example.com"} {
		f.answer(h)
	}
	f.calls.Reset()
	if e := f.answer("e.example.com"); e != addrs[1] {
		t.Fatalf("e got %v, want b's %v", e, addrs[1])
	}
	f.wantCalls(append(releaseCalls(addrs[1]), entryCalls("e.example.com", addrs[1])...)...)
}

// TestFreeListOrder checks that released addresses are given out first
// in, first out, and only after every fresh address.
func TestFreeListOrder(t *testing.T) {
	f := newFixture(t, "198.18.0.0/30")
	a := f.answer("a.example.com")
	f.clock.advance(time.Second)
	b := f.answer("b.example.com")
	f.clock.advance(expire - time.Second)
	f.p.Sweep(context.Background()) // releases a only
	f.clock.advance(time.Second)
	f.p.Sweep(context.Background()) // then b
	got := []netip.Addr{
		f.answer("c.example.com"), f.answer("d.example.com"),
		f.answer("e.example.com"), f.answer("f.example.com"),
	}
	want := []netip.Addr{addr("198.18.0.2"), addr("198.18.0.3"), a, b}
	if !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// TestKept checks that a pool started with kept entries gives none of
// their addresses to another name before it has released them.
func TestKept(t *testing.T) {
	kept := []Entry{
		{Name: nt.Name("kept.example.com"), Addr: addr("198.18.0.2")},
		{Name: nt.Name("old.example.com"), Addr: addr("198.18.0.0")},
	}
	f := newFixture(t, "198.18.0.0/30", kept...)
	if got := f.p.Live(); got[0] != kept[1] || got[1] != kept[0] {
		t.Fatalf("live %v", got)
	}
	if got := []netip.Addr{f.answer("a.example.com"), f.answer("b.example.com")}; !slices.Equal(got, []netip.Addr{addr("198.18.0.1"), addr("198.18.0.3")}) {
		t.Fatalf("new names got %v", got)
	}
	f.refused("c.example.com", netd.RuleSyntheticPoolFull)
	f.clock.advance(time.Second)
	if a := f.answer("kept.example.com"); a != addr("198.18.0.2") {
		t.Fatalf("a kept name moved to %v", a)
	}

	f.clock.advance(expire - time.Second - time.Nanosecond)
	f.refused("c.example.com", netd.RuleSyntheticPoolFull) // not expired yet: counted from the start
	f.clock.advance(time.Nanosecond)
	f.calls.Reset()
	if c := f.answer("c.example.com"); c != addr("198.18.0.0") {
		t.Fatalf("c got %v", c)
	}
	f.wantCalls(append(append(releaseCalls(addr("198.18.0.0")), releaseCalls(addr("198.18.0.1"))...),
		append(releaseCalls(addr("198.18.0.3")), entryCalls("c.example.com", addr("198.18.0.0"))...)...)...)
}

// TestNewRefuses checks that a bad prefix, missing dependencies or a bad
// kept entry refuse the start.
func TestNewRefuses(t *testing.T) {
	calls := &nt.Calls{}
	deps := Deps{Accounting: nt.Accounting{Calls: calls}, Reports: nt.NameReports{Calls: calls}}
	pool := netip.MustParsePrefix("198.18.0.0/15")
	good := Entry{Name: nt.Name("a.example.com"), Addr: addr("198.18.0.1")}
	cases := []struct {
		name   string
		prefix netip.Prefix
		kept   []Entry
		deps   Deps
		want   string
	}{
		{"wider than /15", netip.MustParsePrefix("198.16.0.0/14"), nil, deps, "isn't an IPv4 network"},
		{"not masked", netip.MustParsePrefix("198.18.0.1/15"), nil, deps, "isn't an IPv4 network"},
		{"IPv6", netip.MustParsePrefix("fd00::/112"), nil, deps, "isn't an IPv4 network"},
		{"no accounting", pool, nil, Deps{Reports: deps.Reports}, "needs accounting"},
		{"no reports", pool, nil, Deps{Accounting: deps.Accounting}, "needs accounting"},
		{"outside", pool, []Entry{{Name: good.Name, Addr: addr("198.20.0.1")}}, deps, "outside"},
		{"IPv6 address", pool, []Entry{{Name: good.Name, Addr: addr("::1")}}, deps, "outside"},
		{"zero name", pool, []Entry{{Addr: good.Addr}}, deps, dnsname.RuleForm},
		{"address twice", pool, []Entry{good, {Name: nt.Name("b.example.com"), Addr: good.Addr}}, deps, "kept twice"},
		{"name twice", pool, []Entry{good, {Name: good.Name, Addr: addr("198.18.0.2")}}, deps, "kept twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.prefix, c.kept, c.deps); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("New = %v, want %q", err, c.want)
			}
		})
	}
}

// TestDecideStream checks the stream policy: unmapped addresses,
// disallowed ports and a missing hand-off are refused with their rules,
// and an accepted stream reaches the hand-off with its name and pins the
// entry until the connection closes.
func TestDecideStream(t *testing.T) {
	f := newFixture(t, "198.18.0.0/15")
	a := f.answer("api.example.com")
	f.ports.Allow(nt.Name("api.example.com"), 443)
	flow := func(dest netip.Addr, port uint16) netd.Flow {
		return netd.Flow{Guest: netip.MustParseAddrPort("192.168.127.2:40000"), Dest: netip.AddrPortFrom(dest, port)}
	}
	for _, c := range []struct {
		name string
		f    netd.Flow
		rule netd.Rule
	}{
		{"unmapped", flow(addr("198.18.0.9"), 443), netd.RuleSyntheticUnmapped},
		{"port", flow(a, 22), netd.RulePortNotAllowed},
	} {
		if d := f.p.DecideStream(c.f); d.Sink != nil || d.Rule != c.rule {
			t.Fatalf("%s: %+v, want %q", c.name, d, c.rule)
		}
	}

	d := f.p.DecideStream(flow(a, 443))
	if d.Sink == nil || d.Rule != "" {
		t.Fatalf("accepted: %+v", d)
	}
	ours, theirs := net.Pipe()
	defer func() { _ = theirs.Close() }()
	d.Sink.ServeStream(ours, flow(a, 443))
	s := <-f.handOff
	if s.Flow.Name != nt.Name("api.example.com") || s.Flow.Dest != netip.AddrPortFrom(a, 443) {
		t.Fatalf("handed %+v", s.Flow)
	}
	f.calls.Reset()
	f.clock.advance(expire)
	f.p.Sweep(context.Background())
	f.wantCalls() // pinned
	_ = s.Conn.Close()
	f.p.Sweep(context.Background())
	f.wantCalls(releaseCalls(a)...)
}

// TestServeStreamRechecks checks that a connection whose decision no
// longer holds when the stack serves it is closed, recorded with its
// rule, and not handed off: its entry was released, its address went to
// another name, or its port was taken back during the handshake. The
// close leaves no pin behind.
func TestServeStreamRechecks(t *testing.T) {
	api := nt.Name("api.example.com")
	cases := []struct {
		name   string
		change func(f *fixture)
		rule   netd.Rule
	}{
		{"released", func(f *fixture) {
			f.clock.advance(expire)
			f.p.Sweep(context.Background())
		}, netd.RuleSyntheticUnmapped},
		{"another name", func(f *fixture) {
			f.clock.advance(expire)
			f.p.Sweep(context.Background())
			f.answer("other.example.com")
		}, netd.RuleSyntheticUnmapped},
		{"port taken back", func(f *fixture) { f.ports.Deny(api, 443) }, netd.RulePortNotAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, "198.18.0.0/31")
			a := f.answer("api.example.com")
			if b := f.answer("b.example.com"); b == a {
				t.Fatal("two names share an address")
			}
			f.ports.Allow(api, 443)
			f.ports.Allow(nt.Name("other.example.com"), 443)
			fl := netd.Flow{Dest: netip.AddrPortFrom(a, 443)}
			d := f.p.DecideStream(fl)
			c.change(f)
			ours, theirs := net.Pipe()
			d.Sink.ServeStream(ours, fl)
			if _, err := theirs.Read(make([]byte, 1)); err == nil {
				t.Fatal("the connection is open")
			}
			select {
			case s := <-f.handOff:
				t.Fatalf("handed %+v", s.Flow)
			default:
			}
			if n := f.p.Recorder().Count(c.rule); n != 1 {
				t.Fatalf("%s count %d, want 1", c.rule, n)
			}
			f.clock.advance(expire)
			f.p.Sweep(context.Background())
			if n := f.p.Len(); n != 0 {
				t.Fatalf("%d entries left pinned", n)
			}
		})
	}
}

// TestFullPoolSweepsOnce checks that answers for new names in a full
// pool don't sweep again until the clock moves.
func TestFullPoolSweepsOnce(t *testing.T) {
	f := newFixture(t, "198.18.0.0/31")
	f.answer("a.example.com")
	f.answer("b.example.com")
	f.refused("c.example.com", netd.RuleSyntheticPoolFull)
	if f.p.swept.IsZero() {
		t.Fatal("a full pool didn't sweep")
	}
	swept := f.p.swept
	f.refused("d.example.com", netd.RuleSyntheticPoolFull)
	if f.p.swept != swept {
		t.Fatal("swept again at the same clock reading")
	}
	f.clock.advance(time.Second)
	f.refused("d.example.com", netd.RuleSyntheticPoolFull)
	if !f.p.swept.After(swept) {
		t.Fatal("didn't sweep once the clock moved")
	}
}

// TestDecideStreamUnbound checks a pool with no port policy and one with
// no hand-off.
func TestDecideStreamUnbound(t *testing.T) {
	f := newFixture(t, "198.18.0.0/15")
	a := f.answer("api.example.com")
	fl := netd.Flow{Dest: netip.AddrPortFrom(a, 443)}
	f.p.deps.Ports = nil
	if d := f.p.DecideStream(fl); d.Rule != netd.RulePortNotAllowed {
		t.Fatalf("no port policy: %+v", d)
	}
	f.p.deps.Ports = f.ports
	f.ports.Allow(nt.Name("api.example.com"), 443)
	f.p.deps.HandOff = nil
	if d := f.p.DecideStream(fl); d.Sink != nil || d.Rule != netd.RuleHandoffUnavailable {
		t.Fatalf("no hand-off: %+v", d)
	}
}

// TestDecideStreamDuringSend checks that a decision doesn't wait for an
// answer whose write is in progress.
func TestDecideStreamDuringSend(t *testing.T) {
	f := newFixture(t, "198.18.0.0/15")
	a := f.answer("api.example.com")
	f.calls.Fail(nt.CallEntry, nt.Fault{Delay: time.Second})
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		f.p.Answer(context.Background(), nt.Name("cdn.example.com"))
	}()
	defer func() { <-answered }()
	for len(f.calls.List()) < 3 {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	f.p.DecideStream(netd.Flow{Dest: netip.AddrPortFrom(a, 443)})
	if d := time.Since(start); d > SendTimeout {
		t.Fatalf("the decision took %v", d)
	}
}
