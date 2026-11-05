package hostlink_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/hostlink"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// targets takes what the control channel passes on.
type targets struct {
	replaced chan uint64
	policies chan *policy.Policy
}

func (tg *targets) Replace(conn handoff.Conn, generation uint64) {
	_ = conn.Close()
	tg.replaced <- generation
}

func (tg *targets) ApplyPolicy(p *policy.Policy) { tg.policies <- p }

// control is a Control with a fake wb-hostd at the far end of its
// channel. raw is a copy of wb-hostd's end, for a send fdchan's Send
// doesn't make.
type control struct {
	c     *hostlink.Control
	hostd *nt.Peer
	raw   rawEnd
	tg    *targets
}

// rawEnd is a copy of wb-hostd's descriptor, for a send fdchan's Send
// doesn't make. Close closes it once.
type rawEnd struct {
	fd    int
	Close func()
}

// rawPair returns two connected fdchan channels, wb-hostd's end and
// wb-netd's, and a copy of wb-hostd's descriptor.
func rawPair(t *testing.T) (hostd, ours *fdchan.Channel, raw rawEnd) {
	t.Helper()
	fa, fb, err := fdchan.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Dup(int(fa.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	raw = rawEnd{fd: fd, Close: sync.OnceFunc(func() { _ = syscall.Close(fd) })}
	t.Cleanup(raw.Close)
	hostd, err = fdchan.New(fa, egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	ours, err = fdchan.New(fb, egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostd.Close(); _ = ours.Close() })
	return hostd, ours, raw
}

// sendTwo sends rec with two descriptors on raw. fdchan refuses it at
// the far end.
func sendTwo(t *testing.T, raw rawEnd, rec []byte) {
	t.Helper()
	a, b := datagramEnd(t), datagramEnd(t)
	if err := syscall.Sendmsg(raw.fd, rec, syscall.UnixRights(int(a.Fd()), int(b.Fd())), nil, 0); err != nil {
		t.Fatal(err)
	}
}

func newControl(t *testing.T) *control { return newControlTimeout(t, 0) }

// newControlTimeout returns a control whose wait for wb-hostd's version
// record is bounded by d, zero for hostversion.Timeout.
func newControlTimeout(t *testing.T, d time.Duration) *control {
	t.Helper()
	hostd, ours, raw := rawPair(t)
	tg := &targets{replaced: make(chan uint64, 8), policies: make(chan *policy.Policy, 8)}
	c, err := hostlink.NewControl(hostlink.ControlConfig{
		Conn: ours, Own: nt.Own, Log: slog.New(slog.DiscardHandler), Handoff: tg, Policy: tg,
		VersionTimeout: d,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &control{c: c, hostd: nt.NewPeer(t, hostd), raw: raw, tg: tg}
}

// withPayload sends m with the read end of a payload pipe, and writes
// payload to the pipe.
func withPayload(t *testing.T, p *nt.Peer, m proto.Message, payload proto.Message) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p.Send(m, r)
	_ = r.Close()
	go func() {
		_ = egressmsg.WritePayload(w, payload)
		_ = w.Close()
	}()
}

// started returns a Control that read a NetdStart with kept as its
// mapping, and what it kept.
func started(t *testing.T, kept []synth.Entry) (*control, hostlink.Kept) {
	t.Helper()
	c := newControl(t)
	type result struct {
		k   hostlink.Kept
		err error
	}
	done := make(chan result, 1)
	go func() {
		k, err := c.c.Start()
		done <- result{k, err}
	}()
	c.hostd.Hello()
	payload := &egressv1.NetdStartPayload{Debited: []string{"x.example.com"}, DebitCount: 4}
	for _, e := range kept {
		wire, _ := egressmsg.AddrToWire(e.Addr)
		payload.Entries = append(payload.Entries, &egressv1.MappingEntry{Name: e.Name.String(), Address: wire})
	}
	withPayload(t, c.hostd, netdStart(), payload)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	return c, r.k
}

// start returns what a Control kept of a NetdStart with kept as its
// mapping.
func start(t *testing.T, kept []synth.Entry) hostlink.Kept {
	t.Helper()
	_, k := started(t, kept)
	return k
}

// serve runs Serve and returns the channel its error comes on.
func (c *control) serve(t *testing.T) chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- c.c.Serve(ctx) }()
	return done
}

// TestStart checks that NetdStart's VM, generation, kept mapping and
// wildcard budget come back from Start.
func TestStart(t *testing.T) {
	kept := []synth.Entry{{Name: nt.Name("kept.example.com"), Addr: netip.MustParseAddr("198.18.0.5")}}
	k := start(t, kept)
	if k.VM != vm || k.Generation != gen || len(k.Entries) != 1 || k.Entries[0] != kept[0] ||
		len(k.Debited) != 1 || k.Debited[0] != nt.Name("x.example.com") || k.DebitCount != 4 {
		t.Fatalf("kept %+v", k)
	}
}

// TestStartRefused checks that a start that isn't wb-netd's ends the
// channel: a first message that isn't NetdStart, or a payload whose
// mapping fails its check.
func TestStartRefused(t *testing.T) {
	for name, send := range map[string]func(t *testing.T, p *nt.Peer){
		"policy change first": func(t *testing.T, p *nt.Peer) {
			withPayload(t, p, policyChange(1), validPolicy())
		},
		"address outside the range": func(t *testing.T, p *nt.Peer) {
			withPayload(t, p, netdStart(), &egressv1.NetdStartPayload{Entries: []*egressv1.MappingEntry{
				{Name: "a.example.com", Address: 0x0a000001},
			}})
		},
		"no payload": func(t *testing.T, p *nt.Peer) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			p.Send(netdStart(), r)
			_ = r.Close()
			_ = w.Close()
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := newControl(t)
			done := make(chan error, 1)
			go func() {
				_, err := c.c.Start()
				done <- err
			}()
			c.hostd.Hello()
			send(t, c.hostd)
			if err := <-done; err == nil {
				t.Fatal("the start was taken")
			}
			if n := c.c.Recorder().Count(netd.RuleControlMessageRefused); n != 1 {
				t.Errorf("control-message-refused counted %d, want 1", n)
			}
		})
	}
}

// datagramEnd returns one end of a new datagram socketpair, whose other
// end is closed at the test's end.
func datagramEnd(t *testing.T) *os.File {
	t.Helper()
	a, b, err := fdchan.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a
}

// TestHandoffSocketRefused checks that a hand-off socket with another
// VM, another generation, or a descriptor that isn't an AF_UNIX
// SOCK_DGRAM socket is refused and logged, and the channel goes on to
// take a good one (S04-07-supervision, "New hand-off sockets").
func TestHandoffSocketRefused(t *testing.T) {
	c, _ := started(t, nil)
	done := c.serve(t)
	stream, other, err := fdchan.StreamPair()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close(); _ = other.Close() }()
	c.hostd.Send(handoffSocket("isolated", gen), datagramEnd(t))
	c.hostd.Send(handoffSocket(vm, gen+1), datagramEnd(t))
	c.hostd.Send(handoffSocket(vm, gen), stream)
	c.hostd.Send(handoffSocket(vm, gen), datagramEnd(t))
	select {
	case g := <-c.tg.replaced:
		if g != gen {
			t.Fatalf("replaced with generation %d", g)
		}
	case err := <-done:
		t.Fatalf("the channel ended: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("the good socket wasn't taken")
	}
	if n := c.c.Recorder().Count(netd.RuleHandoffSocketRefused); n != 3 {
		t.Errorf("handoff-socket-refused counted %d, want 3", n)
	}
	if len(c.tg.replaced) != 0 {
		t.Error("a refused socket was taken")
	}
}

// TestControlEnds checks that a control message of the wrong form or
// order ends the channel, logged with control-message-refused: one with
// two descriptors, a second NetdStart, and a policy change whose
// sequence doesn't increase, and a policy that FromProto refuses. No
// hand-off socket is taken from it.
func TestControlEnds(t *testing.T) {
	for name, send := range map[string]func(t *testing.T, c *control){
		"two descriptors": func(t *testing.T, c *control) {
			rec, err := egressmsg.Marshal(handoffSocket(vm, gen))
			if err != nil {
				t.Fatal(err)
			}
			sendTwo(t, c.raw, rec)
		},
		"second start": func(t *testing.T, c *control) {
			withPayload(t, c.hostd, netdStart(), &egressv1.NetdStartPayload{})
		},
		"sequence repeats": func(t *testing.T, c *control) {
			withPayload(t, c.hostd, policyChange(2), validPolicy())
			withPayload(t, c.hostd, policyChange(2), validPolicy())
		},
		"policy refused": func(t *testing.T, c *control) {
			withPayload(t, c.hostd, policyChange(1), &policyv1.EffectivePolicy{Endpoints: []*policyv1.Endpoint{{Host: "no host"}}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := started(t, nil)
			done := c.serve(t)
			send(t, c)
			ended(t, done)
			if n := c.c.Recorder().Count(netd.RuleControlMessageRefused); n != 1 {
				t.Errorf("control-message-refused counted %d, want 1", n)
			}
			if len(c.tg.replaced) != 0 {
				t.Error("a hand-off socket was taken")
			}
		})
	}
}

// TestPolicyChange checks that a policy change is applied and then
// acknowledged with its sequence (S07-05-stream-hand-off, "Policy
// order").
func TestPolicyChange(t *testing.T) {
	c, _ := started(t, nil)
	c.serve(t)
	withPayload(t, c.hostd, policyChange(5), validPolicy())
	select {
	case p := <-c.tg.policies:
		if p.Generation != 1 {
			t.Fatalf("applied %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the policy wasn't applied")
	}
	m := c.hostd.Next(time.Second)
	defer func() { _ = m.Close() }()
	ack, ref := egressmsg.DecodeNetdToHostd(m.Record, m.File != nil)
	if ref != nil {
		t.Fatal(ref)
	}
	if a := ack.GetPolicyAck(); a.GetVm() != vm || a.GetSequence() != 5 {
		t.Fatalf("got %v, want the ack of sequence 5", ack)
	}
}

// ended waits for Serve to return, and fails the test when it returns
// no error or doesn't return.
func ended(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve returned no error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the channel goes on")
	}
}

// TestServeStops checks that Serve returns when its context ends or
// wb-hostd closes the channel, and logs neither as a refused message.
func TestServeStops(t *testing.T) {
	for name, stop := range map[string]func(c *control, cancel context.CancelFunc){
		"context ends":    func(_ *control, cancel context.CancelFunc) { cancel() },
		"wb-hostd closes": func(c *control, _ context.CancelFunc) { _ = c.hostd.Ch.Close(); c.raw.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := started(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.c.Serve(ctx) }()
			stop(c, cancel)
			ended(t, done)
			if n := c.c.Recorder().Count(netd.RuleControlMessageRefused); n != 0 {
				t.Errorf("control-message-refused counted %d, want 0", n)
			}
		})
	}
}

// TestStartNoVersion checks that a wb-hostd that sends no version
// record within the version timeout is refused with
// peer-version-timeout, and Start returns (S04-architecture, "The
// version record").
func TestStartNoVersion(t *testing.T) {
	c := newControlTimeout(t, 50*time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := c.c.Start()
		done <- err
	}()
	c.hostd.ReadVersion()
	select {
	case err := <-done:
		if r, ok := errors.AsType[*hostversion.Refusal](err); !ok || r.Rule != hostversion.RuleTimeout {
			t.Fatalf("Start: %v, want a %s refusal", err, hostversion.RuleTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start didn't return")
	}
}
