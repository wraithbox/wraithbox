package hostlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// HandoffTarget takes each new hand-off socket: the handoff.Sender.
type HandoffTarget interface {
	Replace(conn handoff.Conn, generation uint64)
}

// PolicyTarget takes each new effective policy of the VM. wb-netd's
// name and port policies read it.
type PolicyTarget interface {
	ApplyPolicy(p *policy.Policy)
}

// ControlConfig is what a Control needs.
type ControlConfig struct {
	// Conn is the control channel wb-netd inherited.
	Conn Conn
	// Own is the build version the version record carries.
	Own string
	// Log takes the log records. Nil is slog.Default.
	Log *slog.Logger
	// Handoff and Policy take the hand-off sockets and the policies.
	Handoff HandoffTarget
	Policy  PolicyTarget
	// SocketType reports a descriptor's family and type. Nil is
	// fdchan.SocketType.
	SocketType func(syscall.Conn) (family, sockType int, err error)
	// NewHandoffConn makes a hand-off socket of a descriptor, and closes
	// the descriptor. Nil is fdchan.New with egressmsg.MaxRecord.
	NewHandoffConn func(f *os.File) (handoff.Conn, error)
}

// Kept is what NetdStart gives a new wb-netd: its VM and generation, the
// kept name mapping and the wildcard budget's state (S04-07-supervision,
// "The kept name mapping"). The pool holds each kept address for its
// name until it is released (synth.New).
type Kept struct {
	VM         string
	Generation uint64
	Entries    []synth.Entry
	Debited    []dnsname.Name
	DebitCount int
}

// Control serves wb-netd's control channel. Start reads the start
// message, then Serve takes the hand-off sockets and the policy changes
// until the channel ends. A message of the wrong form or order, or a
// policy change wb-netd can't apply, ends the channel (control.proto).
// A hand-off socket that isn't wb-netd's is refused, and the channel
// goes on (S04-07-supervision, "New hand-off sockets").
type Control struct {
	cfg   ControlConfig
	rec   *netd.Recorder
	order egressmsg.StartOrder
	kept  Kept
	seq   uint64 // the last policy change's sequence
}

// NewControl returns a Control for cfg.
func NewControl(cfg ControlConfig) (*Control, error) {
	if cfg.Conn == nil || cfg.Handoff == nil || cfg.Policy == nil {
		return nil, errors.New("hostlink: a control channel needs Conn, Handoff and Policy")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.SocketType == nil {
		cfg.SocketType = fdchan.SocketType
	}
	if cfg.NewHandoffConn == nil {
		cfg.NewHandoffConn = func(f *os.File) (handoff.Conn, error) {
			return fdchan.New(f, egressmsg.MaxRecord)
		}
	}
	return &Control{cfg: cfg, rec: netd.NewRecorder(cfg.Log, nil)}, nil
}

// Recorder returns the control channel's recorder, for its counts.
func (c *Control) Recorder() *netd.Recorder { return c.rec }

// Start exchanges version records, then reads NetdStart and its payload
// and returns what it kept. An error ends the channel.
func (c *Control) Start() (Kept, error) {
	if err := c.cfg.Conn.Send(hostversion.Record(c.cfg.Own), nil); err != nil {
		return Kept{}, c.fail("send-version-record", err)
	}
	m, err := c.cfg.Conn.Recv()
	if err != nil {
		return Kept{}, c.fail("recv", err)
	}
	hasFD := m.File != nil
	_ = m.Close()
	if hasFD {
		return Kept{}, c.fail("version-record", errors.New("the version record carries a descriptor"))
	}
	if _, err := hostversion.CheckRecord(c.cfg.Log, c.cfg.Own, m.Record); err != nil {
		return Kept{}, c.fail("version-record", err)
	}
	msg, file, err := c.next()
	if err != nil {
		return Kept{}, err
	}
	defer func() { _ = file.Close() }()
	start := msg.GetNetdStart()
	if start == nil {
		// StartOrder refuses any other first message, so this isn't
		// reached. It fails closed if it ever is.
		return Kept{}, c.fail("message-form", errors.New("the first message isn't netd_start"))
	}
	return c.start(start, file)
}

// start reads the NetdStartPayload of a NetdStart.
func (c *Control) start(m *egressv1.NetdStart, file *os.File) (Kept, error) {
	p := &egressv1.NetdStartPayload{}
	if ref := egressmsg.ReadPayload(file, p); ref != nil {
		return Kept{}, c.fail(string(ref.Check), ref)
	}
	if ref := egressmsg.CheckNetdStart(p); ref != nil {
		return Kept{}, c.fail(string(ref.Check), ref)
	}
	k := Kept{VM: m.GetVm(), Generation: m.GetGeneration(), DebitCount: int(p.GetDebitCount())}
	for _, e := range p.GetEntries() {
		// CheckNetdStart checked each name and address.
		name, _ := egressmsg.Name("entries.name", e.GetName())
		k.Entries = append(k.Entries, synth.Entry{Name: name, Addr: egressmsg.AddrFromWire(e.GetAddress())})
	}
	for _, s := range p.GetDebited() {
		name, _ := egressmsg.Name("debited", s)
		k.Debited = append(k.Debited, name)
	}
	c.kept = k
	c.cfg.Log.LogAttrs(context.Background(), slog.LevelInfo, "netd start",
		slog.String("vm", k.VM), slog.Uint64("generation", k.Generation),
		slog.Int("entries", len(k.Entries)), slog.Int("debited", len(k.Debited)), slog.Int("debit_count", k.DebitCount))
	return k, nil
}

// Serve takes hand-off sockets and policy changes until the channel
// ends, or ctx does, and returns why. It closes the channel.
func (c *Control) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = c.cfg.Conn.Close() })
	defer stop()
	defer func() { _ = c.cfg.Conn.Close() }()
	for {
		msg, file, err := c.next()
		if err != nil {
			return err
		}
		switch k := msg.GetKind().(type) {
		case *egressv1.HostdToNetd_HandoffSocket:
			c.handoffSocket(k.HandoffSocket, file)
		case *egressv1.HostdToNetd_PolicyChange:
			err = c.policyChange(ctx, k.PolicyChange, file)
		}
		_ = file.Close()
		if err != nil {
			return err
		}
	}
}

// next reads and checks the next control message. Every kind carries
// one descriptor, which the caller closes.
func (c *Control) next() (*egressv1.HostdToNetd, *os.File, error) {
	m, err := c.cfg.Conn.Recv()
	if err != nil {
		if refusal, ok := errors.AsType[*fdchan.Refusal](err); ok {
			// fdchan logged it and closed its descriptors.
			return nil, nil, c.fail(string(refusal.Rule), err)
		}
		return nil, nil, c.fail("recv", err)
	}
	msg, ref := egressmsg.DecodeHostdToNetd(m.Record, m.File != nil)
	if ref == nil {
		ref = c.order.NextNetd(msg)
	}
	if ref != nil {
		_ = m.Close()
		return nil, nil, c.fail(string(ref.Check), ref)
	}
	return msg, m.File, nil
}

// handoffSocket takes a new hand-off socket, or refuses it when its VM
// or generation isn't wb-netd's own or its descriptor isn't an AF_UNIX
// SOCK_DGRAM socket (S04-07-supervision, "New hand-off sockets"). A
// refusal closes the descriptor and is logged with
// handoff-socket-refused. The channel goes on.
func (c *Control) handoffSocket(m *egressv1.HandoffSocket, file *os.File) {
	attrs := []slog.Attr{
		slog.String("vm", m.GetVm()), slog.Uint64("generation", m.GetGeneration()),
	}
	refuse := func(check string, err error) {
		_ = file.Close()
		c.rec.Note(netd.RuleHandoffSocketRefused, append(attrs,
			slog.String("check", check), slog.String("decision", "refuse"), slog.String("error", err.Error()))...)
	}
	switch {
	case m.GetVm() != c.kept.VM:
		refuse("vm", fmt.Errorf("another VM than %s", c.kept.VM))
		return
	case m.GetGeneration() != c.kept.Generation:
		refuse("generation", fmt.Errorf("another generation than %d", c.kept.Generation))
		return
	}
	family, sockType, err := c.cfg.SocketType(file)
	if err == nil && (family != syscall.AF_UNIX || sockType != syscall.SOCK_DGRAM) {
		err = fmt.Errorf("family %d, type %d, want AF_UNIX SOCK_DGRAM", family, sockType)
	}
	if err != nil {
		refuse("socket-type", err)
		return
	}
	conn, err := c.cfg.NewHandoffConn(file)
	if err != nil {
		refuse("socket", err)
		return
	}
	c.cfg.Handoff.Replace(conn, m.GetGeneration())
	c.cfg.Log.LogAttrs(context.Background(), slog.LevelInfo, "netd hand-off socket accepted",
		append(attrs, slog.String("decision", "accept"))...)
}

// policyChange reads a new effective policy, applies it, and sends the
// PolicyAck (S07-05-stream-hand-off, "Policy order"). A change for
// another VM, with a sequence that doesn't increase, or with a payload
// or policy that is refused ends the channel.
func (c *Control) policyChange(ctx context.Context, m *egressv1.PolicyChange, file *os.File) error {
	switch {
	case m.GetVm() != c.kept.VM:
		return c.fail("vm", fmt.Errorf("a policy change for %q, not %s", m.GetVm(), c.kept.VM))
	case m.GetSequence() <= c.seq:
		return c.fail("sequence", fmt.Errorf("sequence %d after %d", m.GetSequence(), c.seq))
	}
	ep := &policyv1.EffectivePolicy{}
	if ref := egressmsg.ReadPayload(file, ep); ref != nil {
		return c.fail(string(ref.Check), ref)
	}
	p, pref := policy.FromProto(ctx, ep)
	if pref != nil {
		return c.fail(pref.Rule, pref)
	}
	c.seq = m.GetSequence()
	c.cfg.Policy.ApplyPolicy(p)
	ack, err := egressmsg.Marshal(&egressv1.NetdToHostd{Kind: &egressv1.NetdToHostd_PolicyAck{
		PolicyAck: &egressv1.PolicyAck{Vm: c.kept.VM, Sequence: m.GetSequence()},
	}})
	if err == nil {
		err = c.cfg.Conn.Send(ack, nil)
	}
	if err != nil {
		return c.fail("send-policy-ack", err)
	}
	return nil
}

// fail logs why the control channel ends, with the rule
// control-message-refused and the check, closes it, and returns the
// error.
func (c *Control) fail(check string, err error) error {
	c.rec.Note(netd.RuleControlMessageRefused, slog.String("check", check),
		slog.String("decision", "end-channel"), slog.String("error", err.Error()))
	_ = c.cfg.Conn.Close()
	return fmt.Errorf("hostlink: control channel: %s: %w", check, err)
}
