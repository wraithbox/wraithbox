package egress

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
)

// pipes is a fake Socketpair of two pipes, so a test sees which ends
// wb-hostd closed.
type pipes struct {
	mu    sync.Mutex
	pairs [][2]*os.File
	err   error
}

func (p *pipes) socketpair(int) (a, b *os.File, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, nil, p.err
	}
	a, b, err = os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	p.pairs = append(p.pairs, [2]*os.File{a, b})
	return a, b, nil
}

func (p *pipes) last(t *testing.T) (a, b *os.File) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pairs) == 0 {
		t.Fatal("no socketpair was made")
	}
	pair := p.pairs[len(p.pairs)-1]
	return pair[0], pair[1]
}

// payloadPipe is a fake Pipe. wb-hostd gets the write end of a pipe the
// test reads, and the read end of another pipe to send. With closeEarly
// the test's reader is closed before wb-hostd writes.
type payloadPipe struct{ closeEarly bool }

func (pp *payloadPipe) pipe() (r, w *os.File, err error) {
	ours, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	r, other, err := os.Pipe()
	if err != nil {
		_ = ours.Close()
		_ = w.Close()
		return nil, nil, err
	}
	_ = other.Close()
	if pp.closeEarly {
		_ = ours.Close()
		return r, w, nil
	}
	go func() {
		_, _ = io.Copy(io.Discard, ours)
		_ = ours.Close()
	}()
	return r, w, nil
}

func servedGen(s *State, vm string) uint64 {
	gen, _, _ := s.handoffState(vm)
	return gen
}

// isClosed reports whether wb-hostd closed f.
func isClosed(f *os.File) bool { return errors.Is(f.Close(), os.ErrClosed) }

// isOpen reports whether wb-hostd still holds f, without closing it.
func isOpen(f *os.File) bool {
	_, err := f.Stat()
	return err == nil
}

type proxydHarness struct {
	t      *testing.T
	p      *Proxyd
	conn   *fakeConn
	proxyd *ender
	netd   map[string]*ender
	state  *State
	pipes  *pipes
	pay    *payloadPipe
	logs   *syncBuffer
	served chan error
	// sent is the last HandoffSocket a Handoff sent.
	sent *egressv1.HandoffSocket
}

// newProxyd starts a Proxyd on a fake channel whose wb-proxyd sent its
// version record, with the work and isolated VMs started.
func newProxyd(t *testing.T, ackTimeout time.Duration) *proxydHarness {
	t.Helper()
	log, buf := newLog()
	h := &proxydHarness{
		t: t, conn: newFakeConn(), proxyd: newEnder(),
		netd:  map[string]*ender{"work": newEnder(), "isolated": newEnder()},
		state: NewState(StateConfig{Log: log}), pipes: &pipes{}, pay: &payloadPipe{}, logs: buf, served: make(chan error, 1),
	}
	h.state.Start("work", false)
	h.state.Start("isolated", false)
	p, err := NewProxyd(ProxydConfig{
		Conn: h.conn, Own: own, Log: log, Proxyd: h.proxyd, State: h.state, AckTimeout: ackTimeout,
		Netd: func(vm string) Ender {
			if e, ok := h.netd[vm]; ok {
				return e
			}
			return nil
		},
		Socketpair: h.pipes.socketpair,
		Pipe:       h.pay.pipe,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.p = p
	h.conn.expectVersion(t)
	h.conn.push(hostversion.Record(own))
	go func() { h.served <- p.Serve(context.Background()) }()
	t.Cleanup(func() { _ = h.conn.Close(); <-h.served })
	return h
}

func ackOf(vm string, gen uint64) *egressv1.ProxydToHostd {
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_HandoffSocketAck{
		HandoffSocketAck: &egressv1.HandoffSocketAck{Vm: vm, Generation: gen},
	}}
}

func noticeOf(vm string, gen uint64) *egressv1.ProxydToHostd {
	return &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_HandoffSocketClosed{
		HandoffSocketClosed: &egressv1.HandoffSocketClosed{Vm: vm, Generation: gen, Rule: "name-report-malformed", Check: "name-form"},
	}}
}

type handoffResult struct {
	f   *os.File
	err error
}

// handoff starts a Handoff and checks the HandoffSocket message it
// sends.
func (h *proxydHarness) handoff(vm string, gen uint64) chan handoffResult {
	h.t.Helper()
	res := make(chan handoffResult, 1)
	go func() {
		f, err := h.p.Handoff(context.Background(), vm, gen)
		res <- handoffResult{f, err}
	}()
	m, hasFD := h.conn.nextControl(h.t)
	if s := m.GetHandoffSocket(); s.GetVm() != vm || s.GetGeneration() != gen || !hasFD {
		h.t.Fatalf("sent %v (descriptor %v), want a HandoffSocket for %s %d", m, hasFD, vm, gen)
	}
	h.sent = m.GetHandoffSocket()
	return res
}

func result(t *testing.T, res chan handoffResult) handoffResult {
	t.Helper()
	select {
	case r := <-res:
		return r
	case <-time.After(wait):
		t.Fatal("Handoff didn't return")
		return handoffResult{}
	}
}

// acknowledge sends the ack of a hand-off socket, and checks that Handoff returns
// wb-netd's end.
func (h *proxydHarness) acknowledge(vm string, gen uint64) {
	h.t.Helper()
	res := h.handoff(vm, gen)
	h.conn.pushMsg(h.t, ackOf(vm, gen))
	if r := result(h.t, res); r.err != nil || r.f == nil {
		h.t.Fatalf("Handoff: %v", r.err)
	}
}

func (h *proxydHarness) expectServeEnded() {
	h.t.Helper()
	select {
	case err := <-h.served:
		if err == nil {
			h.t.Fatal("Serve returned nil")
		}
		h.served <- err
	case <-time.After(wait):
		h.t.Fatal("Serve didn't end")
	}
}

// TestHandoffAfterAck: wb-netd's end is returned only after wb-proxyd
// acknowledged its end. wb-hostd keeps its copy of wb-proxyd's end until
// the ack shows wb-proxyd holds it, and then closes it
// (S07-05-stream-hand-off, "Transport", "One hand-off socket per VM").
func TestHandoffAfterAck(t *testing.T) {
	h := newProxyd(t, wait)
	res := h.handoff("work", 4)
	proxydEnd, netdEnd := h.pipes.last(t)
	select {
	case r := <-res:
		t.Fatalf("Handoff returned %v before the ack", r)
	case <-time.After(50 * time.Millisecond):
	}
	if !isOpen(proxydEnd) {
		t.Fatal("wb-hostd closed its copy of wb-proxyd's end before the ack")
	}
	h.conn.pushMsg(t, ackOf("work", 4))
	r := result(t, res)
	if r.err != nil || r.f != netdEnd {
		t.Fatalf("Handoff = %v, %v; want wb-netd's end", r.f, r.err)
	}
	if !isClosed(proxydEnd) {
		t.Fatal("wb-hostd kept its copy of wb-proxyd's end")
	}
	if got := servedGen(h.state, "work"); got != 4 {
		t.Fatalf("served generation %d, want 4", got)
	}
}

// TestHandoffNoAck: no ack within the timeout fails the start with
// proxyd-setup-failed and closes both ends. The late ack is logged and
// ignored, and wb-proxyd goes on.
func TestHandoffNoAck(t *testing.T) {
	h := newProxyd(t, 50*time.Millisecond)
	res := h.handoff("work", 1)
	r := result(t, res)
	var se *SetupError
	if !errors.As(r.err, &se) || se.Rule != RuleSetupFailed || se.Check != checkAckTimeout || r.f != nil {
		t.Fatalf("Handoff = %v, %v; want %s %s", r.f, r.err, RuleSetupFailed, checkAckTimeout)
	}
	proxydEnd, netdEnd := h.pipes.last(t)
	if !isClosed(proxydEnd) || !isClosed(netdEnd) {
		t.Fatal("an end is still open")
	}
	expectLog(t, h.logs, `"rule":"proxyd-setup-failed"`, `"decision":"fail-start"`, `"check":"ack-timeout"`, `"vm":"work"`)
	h.conn.pushMsg(t, ackOf("work", 1))
	expectLog(t, h.logs, "late acknowledgment ignored")
	if got := servedGen(h.state, "work"); got != 0 {
		t.Fatalf("a late ack marked generation %d served", got)
	}
	h.acknowledge("work", 2)
	if got := h.proxyd.got(); len(got) != 0 {
		t.Fatalf("wb-proxyd ended with %q", got)
	}
}

// TestHandoffProxydEnds: the end of wb-proxyd's channel while a start
// waits fails the start, and closes wb-netd's end.
func TestHandoffProxydEnds(t *testing.T) {
	h := newProxyd(t, wait)
	res := h.handoff("work", 1)
	close(h.conn.in)
	r := result(t, res)
	expectSetupError(t, r.err, checkProxydEnded)
	if _, netdEnd := h.pipes.last(t); !isClosed(netdEnd) {
		t.Fatal("wb-netd's end is open")
	}
}

// TestHandoffSendFails: a failed send ends wb-proxyd, fails the start,
// and leaves no end open.
func TestHandoffSendFails(t *testing.T) {
	h := newProxyd(t, wait)
	h.conn.mu.Lock()
	h.conn.sendErr = errors.New("broken")
	h.conn.mu.Unlock()
	_, err := h.p.Handoff(context.Background(), "work", 1)
	expectSetupError(t, err, checkSend)
	h.proxyd.expect(t, RuleControlFailed)
	if a, b := h.pipes.last(t); !isClosed(a) || !isClosed(b) {
		t.Fatal("an end is open")
	}
}

// TestHandoffRefusedBeforeSend: a failed socketpair, an older
// generation, and a canceled start fail the start.
func TestHandoffRefusedBeforeSend(t *testing.T) {
	h := newProxyd(t, wait)
	h.pipes.err = errors.New("no descriptors")
	_, err := h.p.Handoff(context.Background(), "work", 1)
	expectSetupError(t, err, checkSocketpair)
	h.pipes.err = nil

	h.acknowledge("work", 5)
	_, err = h.p.Handoff(context.Background(), "work", 4)
	expectSetupError(t, err, checkGenerationOlder)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = h.p.Handoff(ctx, "work", 6)
	expectSetupError(t, err, checkCanceled)
}

func expectSetupError(t *testing.T, err error, check string) {
	t.Helper()
	if se, ok := errors.AsType[*SetupError](err); !ok || se.Rule != RuleSetupFailed || se.Check != check {
		t.Fatalf("error %v, want %s %s", err, RuleSetupFailed, check)
	}
}

// TestControlMessageRefused: a message wb-hostd refuses, an ack it
// didn't ask for, and a CA reply end wb-proxyd with
// control-message-refused, and Serve stops.
func TestControlMessageRefused(t *testing.T) {
	tests := []struct {
		name string
		push func(t *testing.T, c *fakeConn)
	}{
		{"garbage", func(_ *testing.T, c *fakeConn) { c.push([]byte{0xff}) }},
		{"no kind", func(t *testing.T, c *fakeConn) { t.Helper(); c.pushMsg(t, &egressv1.ProxydToHostd{}) }},
		{"unasked ack", func(t *testing.T, c *fakeConn) { t.Helper(); c.pushMsg(t, ackOf("work", 1)) }},
		{"unasked policy ack", func(t *testing.T, c *fakeConn) {
			t.Helper()
			c.pushMsg(t, &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_PolicyAck{PolicyAck: &egressv1.PolicyAck{Vm: "work", Sequence: 1}}})
		}},
		{"CA reply", func(t *testing.T, c *fakeConn) {
			t.Helper()
			c.pushMsg(t, &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_CaRemoved{CaRemoved: &egressv1.CaRemoved{}}})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newProxyd(t, wait)
			tt.push(t, h.conn)
			h.proxyd.expect(t, RuleControlRefused)
			h.expectServeEnded()
			expectLog(t, h.logs, `"rule":"control-message-refused"`, `"decision":"end-sender"`)
		})
	}
}

// TestProxydVersionRefused: another build's version record ends
// wb-proxyd before any message is read.
func TestProxydVersionRefused(t *testing.T) {
	log, _ := newLog()
	conn, end := newFakeConn(), newEnder()
	p, err := NewProxyd(ProxydConfig{Conn: conn, Own: own, Log: log, Proxyd: end, State: NewState(StateConfig{Log: log})})
	if err != nil {
		t.Fatal(err)
	}
	conn.push(hostversion.Record("9.9.9"))
	if err := p.Serve(context.Background()); err == nil {
		t.Fatal("Serve returned nil")
	}
	end.expect(t, hostversion.RuleMismatch)
}

// TestHandoffSocketClosed: a HandoffSocketClosed for the VM's current
// socket ends that VM's wb-netd with name-report-malformed, once. The
// other VM's wb-netd and wb-proxyd go on. A notice for an older socket,
// a repeat, and one for a VM with no acknowledged socket are logged
// with handoff-notice-stale and ignored (S07-06-name-mapping,
// "Malformed reports").
func TestHandoffSocketClosed(t *testing.T) {
	h := newProxyd(t, wait)
	h.acknowledge("work", 1)
	h.acknowledge("work", 2)
	h.conn.pushMsg(t, noticeOf("work", 1))
	expectLog(t, h.logs, `"rule":"handoff-notice-stale"`, `"generation":1`)
	h.conn.pushMsg(t, noticeOf("isolated", 1))
	expectLog(t, h.logs, `"rule":"handoff-notice-stale"`, `"vm":"isolated"`)
	h.conn.pushMsg(t, noticeOf("work", 2))
	h.netd["work"].expect(t, "name-report-malformed")
	expectLog(t, h.logs, `"rule":"name-report-malformed"`, `"decision":"end-sender"`, `"vm":"work"`, `"check":"name-form"`)
	h.conn.pushMsg(t, noticeOf("work", 2))
	expectLog(t, h.logs, `"rule":"handoff-notice-stale"`, `"generation":2`)
	h.acknowledge("isolated", 1)
	if got := h.netd["work"].got(); len(got) != 1 {
		t.Fatalf("wb-netd of work ended %d times", len(got))
	}
	if got := h.netd["isolated"].got(); len(got) != 0 {
		t.Fatalf("wb-netd of isolated ended with %q", got)
	}
	if got := h.proxyd.got(); len(got) != 0 {
		t.Fatalf("wb-proxyd ended with %q", got)
	}
}

// TestHandoffNoticeBeforeAck: a HandoffSocketClosed for the generation
// a Handoff waits on fails that Handoff with proxyd-setup-failed and
// closes both ends, so wb-netd never gets a socket wb-proxyd closed. No
// wb-netd is ended, and the acknowledgment that comes after the notice
// is logged and ignored (S07-05-stream-hand-off, "One hand-off socket
// per VM").
func TestHandoffNoticeBeforeAck(t *testing.T) {
	h := newProxyd(t, wait)
	res := h.handoff("work", 1)
	h.conn.pushMsg(t, noticeOf("work", 1))
	r := result(t, res)
	expectSetupError(t, r.err, checkHandoffClosed)
	if r.f != nil {
		t.Fatal("Handoff returned wb-netd's end")
	}
	proxydEnd, netdEnd := h.pipes.last(t)
	if !isClosed(proxydEnd) || !isClosed(netdEnd) {
		t.Fatal("an end is still open")
	}
	expectLog(t, h.logs, "hand-off notice before its acknowledgment", `"rule":"proxyd-setup-failed"`,
		`"decision":"fail-start"`, `"vm":"work"`, `"generation":1`)
	h.conn.pushMsg(t, ackOf("work", 1))
	expectLog(t, h.logs, "late acknowledgment ignored")
	if got := servedGen(h.state, "work"); got != 0 {
		t.Fatalf("an ack after the notice marked generation %d served", got)
	}
	h.acknowledge("work", 2)
	if got := h.netd["work"].got(); len(got) != 0 {
		t.Fatalf("wb-netd of work ended with %q", got)
	}
	if got := h.proxyd.got(); len(got) != 0 {
		t.Fatalf("wb-proxyd ended with %q", got)
	}
}

// TestConfirm: a HandoffSocketClosed for a VM's acknowledged socket
// while no wb-netd runs for it ends no process, and Confirm then fails
// the start with proxyd-setup-failed. Confirm passes a socket wb-proxyd
// didn't close, and one of an older generation that it did
// (S07-05-stream-hand-off, "One hand-off socket per VM", step 4).
func TestConfirm(t *testing.T) {
	h := newProxyd(t, wait)
	// No wb-netd runs for isolated.
	delete(h.netd, "isolated")
	h.acknowledge("isolated", 1)
	if err := h.p.Confirm("isolated", 1); err != nil {
		t.Fatalf("Confirm before a notice: %v", err)
	}
	h.conn.pushMsg(t, noticeOf("isolated", 1))
	expectLog(t, h.logs, "hand-off socket closed before wb-netd runs", `"rule":"proxyd-setup-failed"`,
		`"decision":"fail-start"`, `"vm":"isolated"`)
	expectSetupError(t, h.p.Confirm("isolated", 1), checkHandoffClosed)
	expectLog(t, h.logs, `"check":"handoff-closed"`, `"vm":"isolated"`)

	h.acknowledge("isolated", 2)
	if err := h.p.Confirm("isolated", 2); err != nil {
		t.Fatalf("Confirm of a new socket: %v", err)
	}
	h.state.Stop("isolated")
	// No wb-netd runs for isolated.
	delete(h.netd, "isolated")
	if err := h.p.Confirm("isolated", 1); err != nil {
		t.Fatalf("Confirm after a new start: %v", err)
	}
	if got := h.proxyd.got(); len(got) != 0 {
		t.Fatalf("wb-proxyd ended with %q", got)
	}
}

// TestHandoffNotStarted: a Handoff for a VM the State doesn't hold as
// started fails the start before anything is sent.
func TestHandoffNotStarted(t *testing.T) {
	h := newProxyd(t, wait)
	h.state.Stop("work")
	_, err := h.p.Handoff(context.Background(), "work", 1)
	expectSetupError(t, err, checkNotStarted)
	_, err = h.p.Handoff(context.Background(), "build", 1)
	expectSetupError(t, err, checkNotStarted)
	select {
	case s := <-h.conn.sent:
		t.Fatalf("sent %v", s)
	default:
	}
}

// TestHandoffPending: a second Handoff for a VM and generation that one
// waits for is refused, and the first still gets its acknowledgment.
func TestHandoffPending(t *testing.T) {
	h := newProxyd(t, wait)
	res := h.handoff("work", 1)
	_, err := h.p.Handoff(context.Background(), "work", 1)
	expectSetupError(t, err, checkPending)
	h.conn.pushMsg(t, ackOf("work", 1))
	if r := result(t, res); r.err != nil {
		t.Fatal(r.err)
	}
}

// TestHandoffRetryAfterTimeout: a retry with the generation of an
// attempt that timed out isn't acknowledged by that attempt's late
// acknowledgment, only by its own.
func TestHandoffRetryAfterTimeout(t *testing.T) {
	h := newProxyd(t, 200*time.Millisecond)
	expectSetupError(t, result(t, h.handoff("work", 1)).err, checkAckTimeout)
	res := h.handoff("work", 1)
	h.conn.pushMsg(t, ackOf("work", 1))
	expectLog(t, h.logs, "late acknowledgment ignored")
	select {
	case r := <-res:
		t.Fatalf("the late acknowledgment ended the retry: %v", r)
	case <-time.After(50 * time.Millisecond):
	}
	h.conn.pushMsg(t, ackOf("work", 1))
	if r := result(t, res); r.err != nil {
		t.Fatal(r.err)
	}
	if got := h.proxyd.got(); len(got) != 0 {
		t.Fatalf("wb-proxyd ended with %q", got)
	}
}

// TestHandoffFresh: the first HandoffSocket after a start that cleared
// the VM's state is fresh, and so is a retry after a timeout. A
// replacement after an acknowledged one and the first after a restore
// aren't (S07-06-name-mapping, "Lifetime").
func TestHandoffFresh(t *testing.T) {
	h := newProxyd(t, 200*time.Millisecond)
	for _, step := range []struct {
		name    string
		restart func()
		gen     uint64
		ack     bool
		fresh   bool
	}{
		{"start", nil, 1, false, true},
		{"retry after a timeout", nil, 1, true, true},
		{"wb-netd restart", nil, 1, true, false},
		{"restore", func() { h.state.Stop("work"); h.state.Start("work", true) }, 2, true, false},
		{"start after a stop", func() { h.state.Stop("work"); h.state.Start("work", false) }, 3, true, true},
	} {
		if step.restart != nil {
			step.restart()
		}
		res := h.handoff("work", step.gen)
		if h.sent.GetFresh() != step.fresh {
			t.Fatalf("%s: fresh = %v, want %v", step.name, h.sent.GetFresh(), step.fresh)
		}
		if !step.ack {
			expectSetupError(t, result(t, res).err, checkAckTimeout)
			// The abandoned attempt's acknowledgment comes late, and the
			// retry ignores it.
			h.conn.pushMsg(t, ackOf("work", step.gen))
			continue
		}
		h.conn.pushMsg(t, ackOf("work", step.gen))
		if r := result(t, res); r.err != nil {
			t.Fatalf("%s: %v", step.name, r.err)
		}
	}
}

// TestNewActivePeriod: wb-hostd clears its own set of the VM's
// wildcard names, then sends wb-proxyd an ActivePeriodStart with no
// descriptor.
func TestNewActivePeriod(t *testing.T) {
	h := newProxyd(t, wait)
	h.acknowledge("work", 1)
	mustRecord(t, h.state, ProxydSource(), proxydNameRecord(t, "work", "x.wild.example.com"))
	if _, _, kept := h.state.Kept("work"); len(kept) != 1 {
		t.Fatalf("wb-proxyd's names before the new period: %v", names(kept))
	}
	if err := h.p.NewActivePeriod("work"); err != nil {
		t.Fatal(err)
	}
	if _, _, kept := h.state.Kept("work"); len(kept) != 0 {
		t.Fatalf("wb-proxyd's names after the new period: %v", names(kept))
	}
	m, hasFD := h.conn.nextControl(t)
	if m.GetActivePeriodStart().GetVm() != "work" || hasFD {
		t.Fatalf("sent %v (descriptor %v), want an ActivePeriodStart for work", m, hasFD)
	}
}

// TestNewActivePeriodSendFails: a failed ActivePeriodStart ends
// wb-proxyd, and isn't logged or returned as a failed VM start.
func TestNewActivePeriodSendFails(t *testing.T) {
	h := newProxyd(t, wait)
	h.conn.mu.Lock()
	h.conn.sendErr = errors.New("broken")
	h.conn.mu.Unlock()
	err := h.p.NewActivePeriod("work")
	var se *SetupError
	if err == nil || errors.As(err, &se) {
		t.Fatalf("NewActivePeriod = %v, want an error that isn't a *SetupError", err)
	}
	h.proxyd.expect(t, RuleControlFailed)
	expectLog(t, h.logs, "new active period's message to wb-proxyd failed", `"rule":"control-channel-failed"`, `"vm":"work"`)
	if strings.Contains(h.logs.String(), "fail-start") {
		t.Fatalf("logged as a failed start:\n%s", h.logs.String())
	}
}
