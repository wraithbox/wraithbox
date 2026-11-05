package handoff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// Conn is one end of a channel: an *fdchan.Channel, or a fake in tests.
type Conn interface {
	Send(record []byte, fd syscall.Conn) error
	Recv() (*fdchan.Message, error)
	Close() error
}

// Receiver is wb-proxyd's end of the control channel from wb-hostd and
// of every VM's hand-off socket. Its methods are safe for concurrent
// use.
type Receiver struct {
	cfg Config
	log *slog.Logger

	mu     sync.Mutex
	ctl    Conn
	vms    map[string]*vmState
	closed bool
	// wg counts the goroutines of the hand-off sockets.
	wg sync.WaitGroup
}

// vmState is what the receiver holds for one VM slot. The receiver's
// lock guards it.
type vmState struct {
	slot string
	// generation is the generation of the newest hand-off socket
	// accepted for the VM, 0 before the first.
	generation uint64
	// policy is the VM's effective policy, nil before the first
	// PolicyChange. A nil policy allows no name, so every report and
	// stream is refused until one arrives (SEC05-default-deny).
	policy *policy.Policy
	// sequence is the sequence of the last PolicyChange applied.
	sequence uint64
	copy     *mapping
	// sock is the current hand-off socket, or nil.
	sock *socket
}

// NewReceiver checks cfg and makes a receiver. Serve runs it.
func NewReceiver(cfg Config) (*Receiver, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	window := c.LogWindow
	if window == 0 {
		window = ratelog.Window
	}
	return &Receiver{
		cfg: c,
		log: ratelog.NewAt(c.Log, window, time.Now),
		vms: map[string]*vmState{},
	}, nil
}

// Serve runs the control channel from wb-hostd until it ends: the
// version record each way, then ProxydStart, then HandoffSocket and
// PolicyChange messages in the order they arrive. It refuses a message
// as control.proto says and then ends the channel. When it returns it
// closes every hand-off socket and every stream: with no control
// channel a narrowing policy can't arrive, so nothing may carry on
// under the old one (SEC05-default-deny). It returns nil when wb-hostd
// closed the channel.
func (r *Receiver) Serve(ctx context.Context, ctl Conn) error {
	defer r.Close()
	defer func() { _ = ctl.Close() }()
	r.mu.Lock()
	if r.ctl != nil || r.closed {
		r.mu.Unlock()
		return errors.New("handoff: Serve called twice, or after Close")
	}
	r.ctl = ctl
	r.mu.Unlock()
	if err := r.exchangeVersions(ctl); err != nil {
		return err
	}
	var order egressmsg.StartOrder
	for {
		m, err := ctl.Recv()
		if ref, ok := errors.AsType[*fdchan.Refusal](err); ok {
			// Such as a datagram with two descriptors. fdchan closed
			// them, and its refusal can't say which kind the record was.
			r.refused(Event{Rule: RuleControlRefused, Check: egressmsg.CheckMessageForm},
				slog.String("fdchan_rule", string(ref.Rule)))
			return fmt.Errorf("handoff: control channel: %w", err)
		}
		if err != nil {
			return ended(err)
		}
		if err := r.control(ctx, &order, m); err != nil {
			return err
		}
	}
}

// ended returns nil for end of file and err otherwise.
func ended(err error) error {
	if errors.Is(err, os.ErrClosed) || isEOF(err) {
		return nil
	}
	return fmt.Errorf("handoff: control channel: %w", err)
}

// exchangeVersions sends wb-proxyd's version record and checks that
// wb-hostd's is the first record it reads (S04-architecture, "The
// version record").
func (r *Receiver) exchangeVersions(ctl Conn) error {
	if err := ctl.Send(hostversion.Record(r.cfg.Version), nil); err != nil {
		return fmt.Errorf("handoff: send the version record: %w", err)
	}
	m, err := ctl.Recv()
	if err != nil {
		return fmt.Errorf("handoff: read the version record: %w", err)
	}
	if m.File != nil {
		_ = m.Close()
		r.refused(Event{Rule: RuleControlRefused, Check: CheckVersion},
			slog.String("reason", "the version record carries a descriptor"))
		return errors.New("handoff: the version record carries a descriptor")
	}
	if _, err := hostversion.CheckRecord(r.cfg.Log, r.cfg.Version, m.Record); err != nil {
		r.refused(Event{Rule: RuleControlRefused, Check: CheckVersion})
		return fmt.Errorf("handoff: control channel: %w", err)
	}
	return nil
}

// control decides one message of the control channel.
func (r *Receiver) control(ctx context.Context, order *egressmsg.StartOrder, m *fdchan.Message) error {
	msg, ref := egressmsg.DecodeHostdToProxyd(m.Record, m.File != nil)
	if ref == nil {
		ref = order.NextProxyd(msg)
	}
	if ref != nil {
		_ = m.Close()
		rule := RuleControlRefused
		if controlKindIsHandoffSocket(m.Record) {
			rule = RuleHandoffSocketRefused
		}
		r.refused(Event{Rule: rule, Check: ref.Check}, refusalAttrs(ref)...)
		return ref
	}
	switch k := msg.GetKind().(type) {
	case *egressv1.HostdToProxyd_ProxydStart:
		return r.start(m.File)
	case *egressv1.HostdToProxyd_HandoffSocket:
		return r.addSocket(k.HandoffSocket.GetVm(), k.HandoffSocket.GetGeneration(), m.File)
	case *egressv1.HostdToProxyd_PolicyChange:
		return r.applyPolicy(ctx, k.PolicyChange.GetVm(), k.PolicyChange.GetSequence(), m.File)
	}
	// DecodeHostdToProxyd refuses a record with no kind.
	_ = m.Close()
	return errors.New("handoff: a control message of no kind")
}

// controlKindIsHandoffSocket reports whether a refused control record
// decodes far enough to say it is a HandoffSocket, so its refusal is
// logged with handoff-socket-refused (S07-05-stream-hand-off, "From
// `wb-hostd`"). It only picks the log rule.
func controlKindIsHandoffSocket(record []byte) bool {
	var m egressv1.HostdToProxyd
	_ = proto.Unmarshal(record, &m)
	return m.GetHandoffSocket() != nil
}

// refusalAttrs are an egressmsg refusal's log attributes without the
// check, which refused adds.
func refusalAttrs(ref *egressmsg.Refusal) []slog.Attr {
	return []slog.Attr{slog.String("field", ref.Field), slog.String("reason", ref.Error())}
}

// start applies ProxydStart: the names each VM's second wildcard count
// already holds for its active period (S07-06-name-mapping, "Wildcard
// budget, second layer").
func (r *Receiver) start(f *os.File) error {
	defer func() { _ = f.Close() }()
	var p egressv1.ProxydStartPayload
	ref := egressmsg.ReadPayload(f, &p)
	if ref == nil {
		ref = egressmsg.CheckProxydStart(&p)
	}
	if ref != nil {
		r.refused(Event{Rule: RuleControlRefused, Check: ref.Check}, refusalAttrs(ref)...)
		return ref
	}
	sets := make(map[string][]dnsname.Name, len(p.GetWildcardNames()))
	for _, set := range p.GetWildcardNames() {
		names, ref := canonicalNames(set.GetNames())
		if ref != nil {
			r.refused(Event{VM: set.GetVm(), Rule: RuleControlRefused, Check: ref.Check}, refusalAttrs(ref)...)
			return ref
		}
		sets[set.GetVm()] = append(sets[set.GetVm()], names...)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for slot, names := range sets {
		r.vm(slot).copy.countWildcard(names)
	}
	return nil
}

// vm returns the state of a slot that egressmsg checked, making it
// on first use. The caller holds the lock.
func (r *Receiver) vm(slot string) *vmState {
	st, ok := r.vms[slot]
	if !ok {
		st = &vmState{slot: slot, copy: newMapping()}
		r.vms[slot] = st
	}
	return st
}

// addSocket checks a HandoffSocket and holds its socket as the VM's
// (S07-05-stream-hand-off, "From `wb-hostd`", SEC08-proj-isolation). The
// form and the VM slot are checked; this checks the descriptor's type
// and the generation. It sends wb-proxyd's version record on the new
// socket, then the HandoffSocketAck to wb-hostd. A refusal ends the
// control channel, as control.proto says.
func (r *Receiver) addSocket(slot string, gen uint64, f *os.File) error {
	ch, err := fdchan.New(f, egressmsg.MaxRecord)
	if err != nil {
		r.refused(Event{VM: slot, Generation: gen, Rule: RuleHandoffSocketRefused, Check: CheckSocketType},
			slog.String("reason", err.Error()))
		return fmt.Errorf("handoff: HandoffSocket: %w", err)
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = ch.Close()
		return errors.New("handoff: closed")
	}
	st := r.vm(slot)
	if gen < st.generation {
		held := st.generation
		r.mu.Unlock()
		_ = ch.Close()
		r.refused(Event{VM: slot, Generation: gen, Rule: RuleHandoffSocketRefused, Check: CheckGenerationOlder},
			slog.Uint64("held_generation", held))
		return fmt.Errorf("handoff: HandoffSocket for %s: generation %d is older than %d", slot, gen, held)
	}
	old := st.sock
	st.generation = gen
	s := newSocket(r, st, gen, ch)
	st.sock = s
	r.mu.Unlock()
	if old != nil {
		r.log.LogAttrs(context.Background(), slog.LevelInfo, "hand-off socket replaced",
			slog.String("vm", slot), slog.Uint64("old_generation", old.gen), slog.Uint64("generation", gen))
		old.close()
	}
	if err := ch.Send(hostversion.Record(r.cfg.Version), nil); err != nil {
		s.close()
		return fmt.Errorf("handoff: send the version record on the hand-off socket of %s: %w", slot, err)
	}
	if !s.run() {
		return errors.New("handoff: closed")
	}
	return r.sendHostd(&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_HandoffSocketAck{
		HandoffSocketAck: &egressv1.HandoffSocketAck{Vm: slot, Generation: gen},
	}})
}

// applyPolicy applies a PolicyChange (S07-05-stream-hand-off, "Policy
// order"): it reads and checks the policy, rechecks each open stream of
// the VM and resets those the policy no longer allows
// (S07-01-per-vm-policy, "Open streams"), and then acknowledges.
func (r *Receiver) applyPolicy(ctx context.Context, slot string, seq uint64, f *os.File) error {
	p, err := r.readPolicy(ctx, slot, f)
	if err != nil {
		return err
	}
	r.mu.Lock()
	st := r.vm(slot)
	if seq <= st.sequence {
		held := st.sequence
		r.mu.Unlock()
		r.refused(Event{VM: slot, Rule: RuleControlRefused, Check: CheckSequence},
			slog.Uint64("sequence", seq), slog.Uint64("held_sequence", held))
		return fmt.Errorf("handoff: PolicyChange for %s: sequence %d isn't above %d", slot, seq, held)
	}
	st.policy = p
	st.sequence = seq
	var open []*Stream
	if st.sock != nil {
		open = st.sock.openStreams()
	}
	r.mu.Unlock()
	for _, s := range open {
		if check := s.recheck(p); check != "" {
			r.refused(Event{VM: slot, Generation: s.Generation, Rule: RulePolicyRecomputed, Check: check},
				streamAttrs(s)...)
			s.Reset()
		}
	}
	return r.sendHostd(&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_PolicyAck{
		PolicyAck: &egressv1.PolicyAck{Vm: slot, Sequence: seq},
	}})
}

// readPolicy reads a PolicyChange's payload and checks it with
// policy.FromProto.
func (r *Receiver) readPolicy(ctx context.Context, slot string, f *os.File) (*policy.Policy, error) {
	defer func() { _ = f.Close() }()
	var m policyv1.EffectivePolicy
	if ref := egressmsg.ReadPayload(f, &m); ref != nil {
		r.refused(Event{VM: slot, Rule: RuleControlRefused, Check: ref.Check}, refusalAttrs(ref)...)
		return nil, ref
	}
	p, pref := policy.FromProto(ctx, &m)
	if pref != nil {
		r.refused(Event{VM: slot, Rule: RuleControlRefused, Check: CheckPolicy},
			slog.String("policy_rule", pref.Rule), slog.String("field", pref.Field))
		return nil, pref
	}
	return p, nil
}

// sendHostd sends a message to wb-hostd on the control channel, in one
// attempt.
func (r *Receiver) sendHostd(m *egressv1.ProxydToHostd) error {
	b, err := egressmsg.Marshal(m)
	if err != nil {
		return fmt.Errorf("handoff: encode a message to wb-hostd: %w", err)
	}
	r.mu.Lock()
	ctl := r.ctl
	r.mu.Unlock()
	if err := ctl.Send(b, nil); err != nil {
		return fmt.Errorf("handoff: send to wb-hostd: %w", err)
	}
	return nil
}

// refused logs a refusal with its rule and check, through ratelog, and
// gives it to the audit hook.
func (r *Receiver) refused(e Event, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+5)
	all = append(all, slog.String("rule", e.Rule), slog.String("decision", "refuse"))
	if e.Check != "" {
		all = append(all, slog.String("check", string(e.Check)))
	}
	if e.VM != "" {
		all = append(all, slog.String("vm", e.VM))
	}
	if e.Generation != 0 {
		all = append(all, slog.Uint64("generation", e.Generation))
	}
	all = append(all, attrs...)
	r.log.LogAttrs(context.Background(), slog.LevelWarn, "egress hand-off refused", all...)
	if r.cfg.Audit != nil {
		r.cfg.Audit.Refused(e)
	}
}

// Close closes every hand-off socket and every stream that came on one,
// and waits for the sockets' goroutines. Serve calls it when the control
// channel ends.
func (r *Receiver) Close() {
	r.mu.Lock()
	r.closed = true
	var socks []*socket
	for _, st := range r.vms {
		if st.sock != nil {
			socks = append(socks, st.sock)
		}
	}
	r.mu.Unlock()
	for _, s := range socks {
		s.close()
	}
	r.wg.Wait()
}
