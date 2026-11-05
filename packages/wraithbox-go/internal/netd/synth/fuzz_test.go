package synth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// peers is wb-hostd's and wb-proxyd's view of the mapping, built from the
// pool's writes and sends, with faults the fuzzer turns on and off. A
// failed entry or report counts as received, and a failed release as
// not, so each failure is the worst case for the pool's order. It checks
// each call against the properties of FuzzPoolOps.
type peers struct {
	t      *testing.T
	hostd  map[netip.Addr]dnsname.Name
	proxyd map[netip.Addr]dnsname.Name
	pins   map[netip.Addr]int
	fail   [4]bool // entry, release, report, report release
	deny   bool    // the port policy allows no port
}

var errFuzz = errors.New("fuzz fault")

func (p *peers) err(i int) error {
	if p.fail[i] {
		return errFuzz
	}
	return nil
}

// add records name at addr in a view, and fails when the view holds
// the address for another name: it was reused before its release.
func (p *peers) add(view map[netip.Addr]dnsname.Name, who string, name dnsname.Name, addr netip.Addr) {
	if old, ok := view[addr]; ok && old != name {
		p.t.Fatalf("%s got %s for %v, which it holds for %s", who, name, addr, old)
	}
	view[addr] = name
}

type fuzzAccounting struct{ *peers }

func (a fuzzAccounting) Entry(_ context.Context, name dnsname.Name, addr netip.Addr) error {
	a.add(a.hostd, "wb-hostd", name, addr)
	return a.err(0)
}

func (a fuzzAccounting) Release(_ context.Context, addr netip.Addr) error {
	if a.pins[addr] > 0 {
		a.t.Fatalf("released %v with %d open connections", addr, a.pins[addr])
	}
	if err := a.err(1); err != nil {
		return err
	}
	delete(a.hostd, addr)
	return nil
}

func (a fuzzAccounting) Debit(context.Context, dnsname.Name) error { return nil }

type fuzzReports struct{ *peers }

func (r fuzzReports) Report(_ context.Context, name dnsname.Name, addr netip.Addr) error {
	r.add(r.proxyd, "wb-proxyd", name, addr)
	return r.err(2)
}

func (r fuzzReports) Release(_ context.Context, addr netip.Addr) error {
	if _, ok := r.hostd[addr]; ok {
		r.t.Fatalf("reported the release of %v before wb-hostd recorded it", addr)
	}
	if err := r.err(3); err != nil {
		return err
	}
	delete(r.proxyd, addr)
	return nil
}

// fuzzPrefix is a pool of 8 addresses, so the fuzzer fills it.
var fuzzPrefix = netip.MustParsePrefix("198.18.0.0/29")

// FuzzPoolOps runs a sequence of operations the guest picks on a pool:
// answers for names, connection opens and closes, stream decisions and
// the serves that follow them, clock steps, sweeps, a port policy that
// takes every port back, and faults of the accounting channel and the
// hand-off socket. A served stream goes on with the name both peers
// hold for its address at serve time. No address
// maps to two names at once, no address goes to another name before its
// release was recorded and reported, no pinned entry is released, and the
// live set never exceeds the pool (S07-03-dns, "Synthetic pool").
func FuzzPoolOps(f *testing.F) {
	f.Add([]byte{0, 1, 0, 2, 3, 200, 5, 0, 1})
	f.Add([]byte{0, 1, 1, 1, 3, 255, 3, 255, 4, 0x0f, 5, 4, 0, 0, 2, 4, 0, 0, 3})
	f.Add([]byte{4, 0x01, 0, 7, 4, 0x00, 0, 7, 4, 0x02, 0, 8, 3, 200, 3, 200, 5})
	f.Add([]byte{0, 0, 0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 0, 7, 0, 8, 3, 250, 3, 250, 4, 0x08, 0, 9, 4, 0, 0, 10})
	f.Add([]byte{0, 1, 6, 0, 6, 0, 7, 0, 3, 8, 5, 0, 7, 0, 2, 0, 3, 8, 5, 0, 0, 2, 4, 0x10, 6, 0})
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 4096 {
			return
		}
		pr := &peers{t: t, hostd: map[netip.Addr]dnsname.Name{}, proxyd: map[netip.Addr]dnsname.Name{}, pins: map[netip.Addr]int{}}
		clk := newClock()
		r := &fuzzRun{t: t, peers: pr, clock: clk}
		p, err := New(fuzzPrefix, nil, Deps{
			Accounting: fuzzAccounting{pr},
			Reports:    fuzzReports{pr},
			Ports:      fuzzPorts{pr},
			HandOff:    fuzzHandOff{r},
			Now:        clk.Now,
			Log:        slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		r.p = p
		for i := 0; i+1 < len(ops); i += 2 {
			r.step(ops[i], ops[i+1])
			r.check()
		}
	})
}

// open is a connection the fuzzer opened.
type open struct {
	addr netip.Addr
	done func()
}

// decided is a stream the pool accepted at its SYN, waiting for the
// stack to serve it.
type decided struct {
	sink netd.StreamSink
	flow netd.Flow
}

type fuzzRun struct {
	t       *testing.T
	p       *Pool
	peers   *peers
	clock   *clock
	opens   []open
	decided []decided
}

type fuzzPorts struct{ *peers }

func (p fuzzPorts) PortAllowed(dnsname.Name, uint16) bool { return !p.deny }

// fuzzHandOff takes a stream the pool served, and fails when its name
// isn't the one both peers hold for its address. The stream is open
// until the fuzzer closes it.
type fuzzHandOff struct{ r *fuzzRun }

func (h fuzzHandOff) HandOffStream(conn netd.StreamConn, f netd.NamedFlow) {
	addr, pr := f.Dest.Addr(), h.r.peers
	if pr.hostd[addr] != f.Name || pr.proxyd[addr] != f.Name {
		h.r.t.Fatalf("handed %v off for %s, which wb-hostd holds for %q and wb-proxyd for %q", addr, f.Name, pr.hostd[addr], pr.proxyd[addr])
	}
	pr.pins[addr]++
	h.r.opens = append(h.r.opens, open{addr: addr, done: func() { _ = conn.Close() }})
}

// fuzzAddr is the address of the pool an argument picks.
func fuzzAddr(arg byte) netip.Addr {
	a := fuzzPrefix.Addr().As4()
	a[3] += arg % 8
	return netip.AddrFrom4(a)
}

// step runs one operation, op, with its argument.
func (r *fuzzRun) step(op, arg byte) {
	switch op % 8 {
	case 0:
		r.answer(arg)
	case 1:
		r.open(arg)
	case 2:
		r.close(arg)
	case 3:
		r.clock.advance(time.Duration(arg) * 15 * time.Second)
	case 4:
		for i := range r.peers.fail {
			r.peers.fail[i] = arg&(1<<i) != 0
		}
		r.peers.deny = arg&(1<<len(r.peers.fail)) != 0
	case 5:
		r.p.Sweep(context.Background())
	case 6:
		r.decide(arg)
	case 7:
		r.serve(arg)
	}
}

// decide asks the stream policy about a SYN to an address, and keeps an
// accepted stream to serve later.
func (r *fuzzRun) decide(arg byte) {
	f := netd.Flow{Dest: netip.AddrPortFrom(fuzzAddr(arg), 443)}
	d := r.p.DecideStream(f)
	if d.Sink != nil {
		r.decided = append(r.decided, decided{sink: d.Sink, flow: f})
	}
}

// serve serves a stream decided earlier, as the stack does once its
// handshake ends. fuzzHandOff checks the name it gets.
func (r *fuzzRun) serve(arg byte) {
	if len(r.decided) == 0 {
		return
	}
	i := int(arg) % len(r.decided)
	d := r.decided[i]
	r.decided = append(r.decided[:i], r.decided[i+1:]...)
	ours, theirs := net.Pipe()
	defer func() { _ = theirs.Close() }()
	d.sink.ServeStream(nt.PipeConn{Conn: ours}, d.flow)
}

func (r *fuzzRun) answer(arg byte) {
	name := nt.Name(fmt.Sprintf("n%d.example.com", arg%16))
	a, rule := r.p.Answer(context.Background(), name)
	if rule != "" {
		return
	}
	if r.peers.hostd[a] != name || r.peers.proxyd[a] != name {
		r.t.Fatalf("answered %s with %v, which wb-hostd holds for %q and wb-proxyd for %q", name, a, r.peers.hostd[a], r.peers.proxyd[a])
	}
}

func (r *fuzzRun) open(arg byte) {
	addr := fuzzAddr(arg)
	name, done, ok := r.p.Open(addr)
	if !ok {
		return
	}
	if r.peers.hostd[addr] != name || r.peers.proxyd[addr] != name {
		r.t.Fatalf("opened %v for %s, which wb-hostd holds for %q and wb-proxyd for %q", addr, name, r.peers.hostd[addr], r.peers.proxyd[addr])
	}
	r.peers.pins[addr]++
	r.opens = append(r.opens, open{addr: addr, done: done})
}

func (r *fuzzRun) close(arg byte) {
	if len(r.opens) == 0 {
		return
	}
	i := int(arg) % len(r.opens)
	o := r.opens[i]
	r.opens = append(r.opens[:i], r.opens[i+1:]...)
	r.peers.pins[o.addr]--
	o.done()
}

// check checks the live set: each address and each name at most once,
// no more entries than the pool has addresses, and each one held by both
// peers.
func (r *fuzzRun) check() {
	live := r.p.Live()
	if len(live) > 1<<(32-fuzzPrefix.Bits()) {
		r.t.Fatalf("%d live entries", len(live))
	}
	names := map[dnsname.Name]bool{}
	for i, e := range live {
		if i > 0 && live[i-1].Addr.Compare(e.Addr) >= 0 {
			r.t.Fatalf("live entries out of order: %v", live)
		}
		if names[e.Name] {
			r.t.Fatalf("%s is live twice: %v", e.Name, live)
		}
		names[e.Name] = true
		if !fuzzPrefix.Contains(e.Addr) {
			r.t.Fatalf("%v is outside the pool", e.Addr)
		}
		if r.peers.hostd[e.Addr] != e.Name || r.peers.proxyd[e.Addr] != e.Name {
			r.t.Fatalf("live %s at %v, which wb-hostd holds for %q and wb-proxyd for %q", e.Name, e.Addr, r.peers.hostd[e.Addr], r.peers.proxyd[e.Addr])
		}
	}
}
