package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// AckTimeout bounds the wait for wb-proxyd's acknowledgment of a
// HandoffSocket or a PolicyChange: the bound of a payload write, so a
// policy wb-proxyd reads whole within its deadline is acknowledged in
// time.
const AckTimeout = egressmsg.PayloadDeadline

// The checks of a proxyd-setup-failed refusal.
const (
	checkSocketpair      = "socketpair"
	checkSend            = "send"
	checkAckTimeout      = "ack-timeout"
	checkProxydEnded     = "proxyd-ended"
	checkCanceled        = "canceled"
	checkGenerationOlder = "generation-older"
	checkPayloadWrite    = "payload-write"
	checkNotStarted      = "vm-not-started"
	checkPending         = "pending"
)

// SetupError is the error of a VM start's message to wb-proxyd that
// failed. It is logged with its rule and check.
type SetupError struct {
	Rule  string
	Check string
	VM    string
	err   error
}

func (e *SetupError) Error() string {
	return "egress: " + e.Rule + ": " + e.Check + " for " + e.VM + ": " + e.err.Error()
}

func (e *SetupError) Unwrap() error { return e.err }

// waitKey names the acknowledgment a call waits for.
type waitKey struct {
	policy bool // a PolicyAck, else a HandoffSocketAck
	vm     string
	n      uint64 // the generation or the sequence
}

// ProxydConfig is what NewProxyd needs.
type ProxydConfig struct {
	// Conn is wb-hostd's end of wb-proxyd's control channel.
	Conn Conn
	// Own is wb-hostd's build version.
	Own string
	// Log takes the log records. Nil is slog.Default.
	Log *slog.Logger
	// Proxyd ends wb-proxyd.
	Proxyd Ender
	// Netd returns the Ender of the VM's wb-netd, and nil when none runs.
	Netd func(vm string) Ender
	// State is where an acknowledged hand-off socket is marked.
	State *State
	// AckTimeout bounds the wait for an acknowledgment. Zero is
	// AckTimeout.
	AckTimeout time.Duration
	// Socketpair makes a hand-off socketpair. Nil is fdchan.Socketpair.
	Socketpair func(maxRecord int) (a, b *os.File, err error)
	// Pipe makes a payload pipe. Nil is os.Pipe.
	Pipe func() (r, w *os.File, err error)
}

// Proxyd is wb-hostd's end of wb-proxyd's control channel
// (S04-architecture, "Control channels"): it sends the start message,
// each VM's policy and hand-off socket, and reads wb-proxyd's
// acknowledgments and HandoffSocketClosed notices. The CA messages are
// #583's. A message it refuses ends wb-proxyd.
type Proxyd struct {
	cfg ProxydConfig
	log *slog.Logger

	sendMu sync.Mutex
	endMu  sync.Once
	done   chan struct{}

	mu      sync.Mutex
	waiters map[waitKey]chan struct{}
	// abandoned counts, per key, the messages whose call gave up before
	// their acknowledgment came. wb-proxyd acknowledges in order, so
	// the next acknowledgments of the key are theirs.
	abandoned map[waitKey]int
}

// NewProxyd sends wb-hostd's version record on the control channel. A
// failed send is returned, and the caller ends wb-proxyd.
func NewProxyd(cfg ProxydConfig) (*Proxyd, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = AckTimeout
	}
	if cfg.Socketpair == nil {
		cfg.Socketpair = fdchan.Socketpair
	}
	if cfg.Pipe == nil {
		cfg.Pipe = os.Pipe
	}
	p := &Proxyd{
		cfg: cfg, log: cfg.Log, done: make(chan struct{}),
		waiters: map[waitKey]chan struct{}{}, abandoned: map[waitKey]int{},
	}
	if err := cfg.Conn.Send(hostversion.Record(cfg.Own), nil); err != nil {
		return nil, fmt.Errorf("egress: send the version record to wb-proxyd: %w", err)
	}
	return p, nil
}

// Serve checks wb-proxyd's version record and reads its messages until
// it closes the channel, a message ends it, or ctx ends. It then closes
// the channel and fails every call that waits.
func (p *Proxyd) Serve(ctx context.Context) error {
	defer close(p.done)
	defer func() { _ = p.cfg.Conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = p.cfg.Conn.Close() })
	defer stop()
	if err := p.version(); err != nil {
		return err
	}
	for {
		m, err := p.cfg.Conn.Recv()
		if err != nil {
			return p.recvFailed(ctx, err)
		}
		if err := p.message(m); err != nil {
			return err
		}
	}
}

// version checks that wb-proxyd's first record is its version record,
// within hostversion.Timeout.
func (p *Proxyd) version() error {
	if err := p.cfg.Conn.SetReadDeadline(time.Now().Add(hostversion.Timeout)); err != nil {
		return p.endProxyd(RuleControlFailed, "deadline", err)
	}
	m, err := p.cfg.Conn.Recv()
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return p.endProxyd(hostversion.RuleTimeout, "version-record", hostversion.RecordTimeout(p.log, p.cfg.Own, err))
	}
	if err != nil {
		return p.endProxyd(RuleControlFailed, "recv", err)
	}
	hasFD := m.File != nil
	_ = m.Close()
	if hasFD {
		return p.endProxyd(RuleControlRefused, CheckDescriptor, errors.New("the version record carries a descriptor"))
	}
	if _, err := hostversion.CheckRecord(p.log, p.cfg.Own, m.Record); err != nil {
		rule := hostversion.RuleMalformed
		if hr, ok := errors.AsType[*hostversion.Refusal](err); ok {
			rule = hr.Rule
		}
		return p.endProxyd(rule, "version-record", err)
	}
	if err := p.cfg.Conn.SetReadDeadline(time.Time{}); err != nil {
		return p.endProxyd(RuleControlFailed, "deadline", err)
	}
	return nil
}

// recvFailed handles an error of Recv. wb-proxyd's exit and the end of
// ctx end the channel without ending wb-proxyd. A datagram the channel
// refuses ends wb-proxyd, and so does another error.
func (p *Proxyd) recvFailed(ctx context.Context, err error) error {
	if ref, ok := errors.AsType[*fdchan.Refusal](err); ok {
		return p.endProxyd(RuleControlRefused, string(ref.Rule), err)
	}
	if errors.Is(err, io.EOF) {
		p.log.Info("wb-proxyd closed its control channel")
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("egress: wb-proxyd's control channel: %w", ctx.Err())
	}
	return p.endProxyd(RuleControlFailed, "recv", err)
}

// message decides one message from wb-proxyd. An acknowledgment no
// call waits for, a CA reply, and a message egressmsg refuses end
// wb-proxyd.
func (p *Proxyd) message(m *fdchan.Message) error {
	msg, ref := egressmsg.DecodeProxydToHostd(m.Record, m.File != nil)
	_ = m.Close()
	if ref != nil {
		return p.endProxyd(RuleControlRefused, string(ref.Check), ref)
	}
	switch k := msg.GetKind().(type) {
	case *egressv1.ProxydToHostd_HandoffSocketAck:
		return p.ack(waitKey{vm: k.HandoffSocketAck.GetVm(), n: k.HandoffSocketAck.GetGeneration()})
	case *egressv1.ProxydToHostd_PolicyAck:
		return p.ack(waitKey{policy: true, vm: k.PolicyAck.GetVm(), n: k.PolicyAck.GetSequence()})
	case *egressv1.ProxydToHostd_HandoffSocketClosed:
		p.closed(k.HandoffSocketClosed)
		return nil
	}
	return p.endProxyd(RuleControlRefused, string(egressmsg.CheckMessageForm),
		errors.New("a CA reply, and wb-hostd sends no CA message yet"))
}

// ack hands an acknowledgment to the call that waits for it. One for a
// message whose call gave up is logged and ignored, and one that no call
// asked for ends wb-proxyd. An acknowledged hand-off socket is marked in
// the State before the call sees it, so a HandoffSocketClosed that
// follows it finds it.
func (p *Proxyd) ack(k waitKey) error {
	p.mu.Lock()
	late := p.abandoned[k] > 0
	w, ok := p.waiters[k]
	switch {
	case late:
		p.abandoned[k]--
		if p.abandoned[k] == 0 {
			delete(p.abandoned, k)
		}
	case ok:
		delete(p.waiters, k)
		if !k.policy {
			p.cfg.State.served(k.vm, k.n)
		}
	}
	p.mu.Unlock()
	switch {
	case late:
		p.log.Info("late acknowledgment ignored", "vm", k.vm, "number", k.n, "policy", k.policy)
		return nil
	case ok:
		close(w)
		return nil
	}
	return p.endProxyd(RuleControlRefused, "unexpected-ack",
		fmt.Errorf("an acknowledgment for %s %d that wb-hostd didn't ask for", k.vm, k.n))
}

// closed acts on a HandoffSocketClosed (S07-06-name-mapping, "Malformed
// reports"): wb-proxyd closed the VM's hand-off socket for a malformed
// message, so wb-hostd ends that VM's wb-netd, which counts toward its
// restart limit. A notice for an older socket, or a second one for the
// same socket, is logged and ignored.
func (p *Proxyd) closed(m *egressv1.HandoffSocketClosed) {
	vm, gen := m.GetVm(), m.GetGeneration()
	attrs := []slog.Attr{
		slog.String("vm", vm), slog.Uint64("generation", gen),
		slog.String("proxyd_rule", m.GetRule()), slog.String("check", m.GetCheck()),
	}
	if !p.cfg.State.endOnce(vm, gen) {
		p.log.LogAttrs(context.Background(), slog.LevelWarn, "hand-off notice ignored",
			append(attrs, slog.String("rule", RuleNoticeStale), slog.String("decision", "ignore"))...)
		return
	}
	end := p.cfg.Netd(vm)
	if end == nil {
		p.log.LogAttrs(context.Background(), slog.LevelWarn, "hand-off notice ignored, no wb-netd runs",
			append(attrs, slog.String("rule", RuleNoticeStale), slog.String("decision", "ignore"))...)
		return
	}
	p.log.LogAttrs(context.Background(), slog.LevelError, "wb-netd ended",
		append(attrs, slog.String("rule", egressmsg.RuleNameReportMalformed), slog.String("decision", "end-sender"))...)
	end.End(egressmsg.RuleNameReportMalformed)
}

// endProxyd logs why wb-proxyd ends and ends it, once.
func (p *Proxyd) endProxyd(rule, check string, err error) error {
	p.endMu.Do(func() {
		p.log.LogAttrs(context.Background(), slog.LevelError, "wb-proxyd ended",
			slog.String("rule", rule), slog.String("decision", "end-sender"), slog.String("check", check),
			slog.String("reason", err.Error()))
		p.cfg.Proxyd.End(rule)
	})
	return fmt.Errorf("egress: %s: %w", rule, err)
}

// Start sends ProxydStart with its payload, the wildcard names of each
// VM's active period (S07-06-name-mapping, "Wildcard budget, second
// layer"). It has no acknowledgment.
func (p *Proxyd) Start(payload *egressv1.ProxydStartPayload) error {
	m := &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_ProxydStart{ProxydStart: &egressv1.ProxydStart{}}}
	return p.sendPayload("", m, payload)
}

// Policy sends vm's effective policy with its sequence number, and
// waits for wb-proxyd's PolicyAck (S07-05-stream-hand-off, "Policy
// order").
func (p *Proxyd) Policy(ctx context.Context, vm string, seq uint64, policy *policyv1.EffectivePolicy) error {
	k := waitKey{policy: true, vm: vm, n: seq}
	w, err := p.expect(k)
	if err != nil {
		return err
	}
	m := &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_PolicyChange{
		PolicyChange: &egressv1.PolicyChange{Vm: vm, Sequence: seq},
	}}
	if err := p.sendPayload(vm, m, policy); err != nil {
		p.forget(k, w)
		return err
	}
	return p.wait(ctx, k, w)
}

// Handoff makes vm's hand-off socketpair for generation gen, at the
// VM's start (S07-05-stream-hand-off, "One hand-off socket per VM"). It
// passes one end to wb-proxyd and waits for the HandoffSocketAck, and
// only then returns the other end, which the caller gives to wb-netd.
// On any failure it closes both ends and returns a *SetupError, and the
// start fails.
//
// The caller calls State.Start for vm first: Handoff refuses a VM that
// isn't started, whose acknowledgment the State would drop. It also
// refuses a second Handoff for the same VM and generation while one
// waits.
func (p *Proxyd) Handoff(ctx context.Context, vm string, gen uint64) (*os.File, error) {
	held, started := p.cfg.State.handoffState(vm)
	if !started {
		return nil, p.setupFailed(vm, checkNotStarted, errors.New("the VM isn't started"))
	}
	if gen < held {
		// wb-proxyd would refuse it and end its control channel.
		return nil, p.setupFailed(vm, checkGenerationOlder, fmt.Errorf("generation %d is older than %d", gen, held))
	}
	k := waitKey{vm: vm, n: gen}
	w, err := p.expect(k)
	if err != nil {
		return nil, err
	}
	proxydEnd, netdEnd, err := p.cfg.Socketpair(egressmsg.MaxRecord)
	if err != nil {
		p.forget(k, w)
		return nil, p.setupFailed(vm, checkSocketpair, err)
	}
	m := &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_HandoffSocket{
		HandoffSocket: &egressv1.HandoffSocket{Vm: vm, Generation: gen},
	}}
	err = p.send(vm, m, proxydEnd)
	_ = proxydEnd.Close()
	if err == nil {
		err = p.wait(ctx, k, w)
	} else {
		p.forget(k, w)
	}
	if err != nil {
		_ = netdEnd.Close()
		return nil, err
	}
	return netdEnd, nil
}

// expect registers a call's wait before its message is sent, so the
// acknowledgment can't come first. A second call for a key that a call
// waits for is refused: their acknowledgments couldn't be told apart.
func (p *Proxyd) expect(k waitKey) (chan struct{}, error) {
	p.mu.Lock()
	_, pending := p.waiters[k]
	w := make(chan struct{})
	if !pending {
		p.waiters[k] = w
	}
	p.mu.Unlock()
	if pending {
		return nil, p.setupFailed(k.vm, checkPending, fmt.Errorf("a call waits for the acknowledgment of %d", k.n))
	}
	return w, nil
}

// forget drops the wait w of a call whose message wasn't sent.
func (p *Proxyd) forget(k waitKey, w chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiters[k] == w {
		delete(p.waiters, k)
	}
}

// wait waits for the acknowledgment of k within the ack timeout. A call
// that gives up counts its message abandoned, so its late
// acknowledgment is ignored and not taken for a later call's.
func (p *Proxyd) wait(ctx context.Context, k waitKey, w chan struct{}) error {
	timer := time.NewTimer(p.cfg.AckTimeout)
	defer timer.Stop()
	var check string
	var cause error
	select {
	case <-w:
		return nil
	case <-timer.C:
		check, cause = checkAckTimeout, fmt.Errorf("no acknowledgment within %v", p.cfg.AckTimeout)
	case <-p.done:
		check, cause = checkProxydEnded, errors.New("wb-proxyd's control channel ended")
	case <-ctx.Done():
		check, cause = checkCanceled, ctx.Err()
	}
	p.mu.Lock()
	waiting := p.waiters[k] == w
	if waiting {
		delete(p.waiters, k)
		p.abandoned[k]++
	}
	p.mu.Unlock()
	if !waiting {
		// The acknowledgment came as the call gave up.
		return nil
	}
	return p.setupFailed(k.vm, check, cause)
}

// sendPayload sends m with the read end of a payload pipe, then writes
// the payload in a goroutine of its own. A write that fails is a crash
// of wb-proxyd (S04-architecture, "Control channels").
func (p *Proxyd) sendPayload(vm string, m *egressv1.HostdToProxyd, payload proto.Message) error {
	r, w, err := p.cfg.Pipe()
	if err != nil {
		return p.setupFailed(vm, checkSend, err)
	}
	err = p.send(vm, m, r)
	_ = r.Close()
	if err != nil {
		_ = w.Close()
		return err
	}
	go func() {
		defer func() { _ = w.Close() }()
		if err := egressmsg.WritePayload(w, payload); err != nil {
			_ = p.endProxyd(RuleControlFailed, checkPayloadWrite, err)
		}
	}()
	return nil
}

// send sends one message. A failed send ends wb-proxyd.
func (p *Proxyd) send(vm string, m *egressv1.HostdToProxyd, fd syscall.Conn) error {
	rec, err := egressmsg.Marshal(m)
	if err != nil {
		return p.setupFailed(vm, checkSend, err)
	}
	p.sendMu.Lock()
	err = p.cfg.Conn.Send(rec, fd)
	p.sendMu.Unlock()
	if err != nil {
		_ = p.endProxyd(RuleControlFailed, checkSend, err)
		return p.setupFailed(vm, checkSend, err)
	}
	return nil
}

func (p *Proxyd) setupFailed(vm, check string, err error) error {
	p.log.LogAttrs(context.Background(), slog.LevelError, "VM start's message to wb-proxyd failed",
		slog.String("rule", RuleSetupFailed), slog.String("decision", "fail-start"), slog.String("check", check),
		slog.String("vm", vm), slog.String("reason", err.Error()))
	return &SetupError{Rule: RuleSetupFailed, Check: check, VM: vm, err: err}
}
