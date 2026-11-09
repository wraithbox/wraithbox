package egress

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
)

// The cross-VM test of C02-streams-and-tls, "Stream hand-off": a real
// wb-proxyd Receiver behind a real Proxyd and Reader, over fdchan
// socketpairs, with the work VM's wb-netd sending a malformed report.

func chanPair(t *testing.T) (a, b *fdchan.Channel) {
	t.Helper()
	fa, fb, err := fdchan.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	return newChan(t, fa), newChan(t, fb)
}

func newChan(t *testing.T, f *os.File) *fdchan.Channel {
	t.Helper()
	ch, err := fdchan.New(f, egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return ch
}

type streams chan *handoff.Stream

func (s streams) HandleStream(st *handoff.Stream) { s <- st }

func endpoint(host string) *policyv1.Endpoint {
	return &policyv1.Endpoint{
		Source:      &policyv1.Source{Kind: policyv1.SourceKind_SOURCE_KIND_GLOBAL},
		RuleName:    "r",
		Host:        host,
		Ports:       []uint32{443},
		Protocol:    policyv1.Protocol_PROTOCOL_L4,
		Enforcement: policyv1.Enforcement_ENFORCEMENT_ENFORCE,
	}
}

// netdEnd is a fake wb-netd on its end of a hand-off socket.
type netdEnd struct {
	t  *testing.T
	ch *fdchan.Channel
}

// newNetdEnd exchanges the version records on wb-netd's end.
func newNetdEnd(t *testing.T, f *os.File) *netdEnd {
	t.Helper()
	n := &netdEnd{t: t, ch: newChan(t, f)}
	if err := n.ch.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatal(err)
	}
	m, err := n.ch.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostversion.CheckRecord(nil, own, m.Record); err != nil {
		t.Fatal(err)
	}
	if err := n.ch.Send(hostversion.Record(own), nil); err != nil {
		t.Fatal(err)
	}
	return n
}

func (n *netdEnd) send(m *egressv1.NetdMessage, fd syscall.Conn) {
	n.t.Helper()
	if err := n.ch.Send(marshal(n.t, m), fd); err != nil {
		n.t.Fatal(err)
	}
}

func (n *netdEnd) report(name string, addr uint32) {
	n.t.Helper()
	n.send(&egressv1.NetdMessage{Kind: &egressv1.NetdMessage_NameReport{
		NameReport: &egressv1.NameReport{Name: name, Address: addr},
	}}, nil)
}

// stream hands over a stream on a new AF_UNIX SOCK_STREAM socketpair.
func (n *netdEnd) stream(gen uint64, addr uint32, id uint64) {
	n.t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		n.t.Fatal(err)
	}
	ours, theirs := os.NewFile(uintptr(fds[0]), "ours"), os.NewFile(uintptr(fds[1]), "theirs")
	n.t.Cleanup(func() { _ = ours.Close() })
	n.send(&egressv1.NetdMessage{Kind: &egressv1.NetdMessage_Stream{
		Stream: &egressv1.Stream{Generation: gen, Address: addr, Port: 443, StreamId: id},
	}}, theirs)
	_ = theirs.Close()
}

// expectEnded checks that wb-proxyd closed the hand-off socket.
func (n *netdEnd) expectEnded() {
	n.t.Helper()
	if err := n.ch.SetReadDeadline(time.Now().Add(wait)); err != nil {
		n.t.Fatal(err)
	}
	for {
		m, err := n.ch.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			n.t.Fatalf("recv: %v, want EOF", err)
		}
		_ = m.Close()
	}
}

// crossVM is wb-hostd's State, Proxyd and wb-proxyd accounting Reader
// around a real wb-proxyd Receiver, with the work and isolated VMs
// started, their policies sent and their hand-off sockets set up.
type crossVM struct {
	state   *State
	logs    *syncBuffer
	streams streams
	proxyd  *ender
	netd    map[string]*ender
	reader  *Reader
	a, b    *netdEnd
}

const (
	genA = 3
	genB = 7
)

func newCrossVM(t *testing.T) *crossVM {
	t.Helper()
	log, logs := newLog()
	c := &crossVM{
		state: NewState(StateConfig{Log: log}), logs: logs, streams: make(streams, 8),
		proxyd: newEnder(), netd: map[string]*ender{"work": newEnder(), "isolated": newEnder()},
	}
	c.state.Start("work", false)
	c.state.Start("isolated", false)

	hostdCtl, proxydCtl := chanPair(t)
	hostdAcct, proxydAcct := chanPair(t)
	// wb-proxyd's accounting writer sends no version record yet (#512),
	// so the test sends it on its behalf.
	if err := proxydAcct.Send(hostversion.Record(own), nil); err != nil {
		t.Fatal(err)
	}
	recv, err := handoff.NewReceiver(handoff.Config{
		Version: own, Handler: c.streams, Wildcard: handoff.AccountingChannel{Ch: proxydAcct}, Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = recv.Serve(context.Background(), proxydCtl) }()
	t.Cleanup(recv.Close)

	c.reader = c.state.Read(hostdAcct, ProxydSource(), c.proxyd, own)
	p, err := NewProxyd(ProxydConfig{
		Conn: hostdCtl, Own: own, Log: log, Proxyd: c.proxyd, State: c.state,
		Netd: func(vm string) Ender { return c.netd[vm] },
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Serve(context.Background()) }()
	if err := p.Start(&egressv1.ProxydStartPayload{}); err != nil {
		t.Fatal(err)
	}
	c.a, c.b = setUp(t, p, "work", genA), setUp(t, p, "isolated", genB)
	return c
}

// setUp sends the VM's hand-off socket and its policy, which allows
// *.wild.example.com on 443, and returns wb-netd's end.
func setUp(t *testing.T, p *Proxyd, vm string, gen uint64) *netdEnd {
	t.Helper()
	ctx := context.Background()
	wild := &policyv1.EffectivePolicy{Generation: 1, WildcardBudget: 4, Endpoints: []*policyv1.Endpoint{endpoint("*.wild.example.com")}}
	// The run's first HandoffSocket is fresh and comes before its first
	// PolicyChange (S07-05-stream-hand-off, "From wb-hostd").
	f, err := p.Handoff(ctx, vm, gen)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Policy(ctx, vm, 1, wild); err != nil {
		t.Fatal(err)
	}
	return newNetdEnd(t, f)
}

// TestMalformedReportEndsOneNetd: VM A's wb-netd sends a name that isn't
// in canonical form. wb-proxyd closes A's hand-off socket and sends
// HandoffSocketClosed, and wb-hostd ends A's wb-netd with
// name-report-malformed. VM B's streams and wb-proxyd's accounting go
// on, and wb-proxyd isn't ended (S07-06-name-mapping, "Malformed
// reports"; SEC08-proj-isolation).
func TestMalformedReportEndsOneNetd(t *testing.T) {
	c := newCrossVM(t)
	c.a.report("A.wild.example.com", wire(1))
	c.a.expectEnded()
	c.netd["work"].expect(t, egressmsg.RuleNameReportMalformed)
	expectLog(t, c.logs, `"rule":"name-report-malformed"`, `"decision":"end-sender"`, `"vm":"work"`, `"generation":3`)

	c.b.report("b.wild.example.com", wire(2))
	c.b.stream(genB, wire(2), 1)
	select {
	case st := <-c.streams:
		defer st.Close()
		if st.VM != "isolated" || st.Name.String() != "b.wild.example.com" {
			t.Fatalf("stream for %s to %s", st.VM, st.Name)
		}
	case <-time.After(wait):
		t.Fatalf("VM B's stream didn't arrive; log:\n%s", c.logs.String())
	}
	expectProxydNames(t, c.state, "isolated", "b.wild.example.com")
	if e := c.netd["isolated"].got(); len(e) != 0 {
		t.Fatalf("VM B's wb-netd ended with %q", e)
	}
	if e := c.proxyd.got(); len(e) != 0 {
		t.Fatalf("wb-proxyd ended with %q", e)
	}
	select {
	case <-c.reader.Done():
		t.Fatal("the reader of wb-proxyd's accounting channel stopped")
	default:
	}
}

// expectProxydNames waits for vm's wb-proxyd wildcard names to be want.
func expectProxydNames(t *testing.T, s *State, vm string, want ...string) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		_, _, got := s.Kept(vm)
		if slices.Equal(names(got), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("wb-proxyd's names for %s: %v, want %v", vm, names(got), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
