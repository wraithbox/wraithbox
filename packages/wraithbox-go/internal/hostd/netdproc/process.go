package netdproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/egress"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/events"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/logpipe"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// proc is one run's wb-netd. It implements vm.NetworkRun, and
// egress.Ender and events.Ender for its channel readers.
type proc struct {
	s          *Starter
	log        *slog.Logger
	slot       vm.Slot
	generation uint64
	vm         eventchan.VM

	cmd    *exec.Cmd
	exited chan struct{}

	control    *fdchan.Channel
	accounting *fdchan.Channel
	events     *fdchan.Channel
	reader     *egress.Reader
	receiver   *events.Receiver
	// stopping is set once Stop closed the control channel, so its
	// reader takes the error that follows as the stop.
	stopping atomic.Bool
}

// channel returns wb-hostd's end of a new channel, and adds the child's
// end to child.
func channel(maxRecord int, child *[]*os.File) (*fdchan.Channel, error) {
	ours, theirs, err := fdchan.Socketpair(maxRecord)
	if err != nil {
		return nil, err
	}
	*child = append(*child, theirs)
	return fdchan.New(ours, maxRecord)
}

// spawn starts wb-netd with the start interface of cmd/wb-netd: no
// arguments, an empty environment, its log on descriptor 2, and the
// control channel, the packet transport, the accounting channel and
// the event channel on 3 to 6. The readers of the accounting and event
// channels start with it. The parent's copies of the child's ends are
// closed in every case.
func (s *Starter) spawn(ctx context.Context, slot vm.Slot, generation uint64, transport childTransport) (*proc, error) {
	p := &proc{
		s: s, log: s.log, slot: slot, generation: generation, exited: make(chan struct{}),
		vm: eventchan.VM{Slot: eventchan.SlotOf(string(slot)), Generation: generation},
	}
	var child []*os.File
	defer func() {
		for _, f := range child {
			_ = f.Close()
		}
	}()
	var err error
	p.control, err = channel(egressmsg.MaxRecord, &child)
	if err == nil {
		p.accounting, err = channel(egressmsg.MaxRecord, &child)
	}
	if err == nil {
		p.events, err = channel(eventchan.MaxRecord, &child)
	}
	if err == nil {
		err = s.closeInherited(ctx, slot, generation)
	}
	var logRead, logWrite *os.File
	if err == nil {
		logRead, logWrite, err = os.Pipe()
	}
	if err != nil {
		p.closeChannels()
		return nil, err
	}
	child = append(child, logWrite)
	cmd := exec.CommandContext(context.WithoutCancel(ctx), s.cfg.Program) //nolint:gosec // G204: the program is the installed bundle's wb-netd, and it gets no arguments.
	cmd.Env = []string{}
	cmd.Stderr = logWrite
	// The packet transport goes in at descriptor 4 as the child starts.
	cmd.ExtraFiles = []*os.File{child[0], nil, child[1], child[2]}
	if err := transport.StartWith(cmd, 1); err != nil {
		_ = logRead.Close()
		p.closeChannels()
		return nil, err
	}
	p.cmd = cmd
	go func() {
		_ = logpipe.Copy(logRead, s.cfg.Logs(Process), Process)
		_ = logRead.Close()
	}()
	go p.wait(ctx)
	s.cfg.Events.Begin(p.vm)
	p.receiver = s.cfg.Events.Attach(p.events, events.Netd(p.vm), p)
	// Every start is a cold start: no kept mapping until warm start.
	s.cfg.Accounting.Start(string(slot), false)
	p.reader = s.cfg.Accounting.Read(p.accounting, egress.NetdSource(string(slot)), p, s.cfg.Own)
	return p, nil
}

// closeInherited marks every descriptor of wb-hostd from 3 on
// close-on-exec before a spawn. wb-hostd may have inherited one without
// the flag from launchd or a shell, and wb-netd refuses any descriptor
// beyond its start interface (S04-09-netd-start, "Start interface",
// SEC12-least-privilege). A walk that fails fails the spawn.
func (s *Starter) closeInherited(ctx context.Context, slot vm.Slot, generation uint64) error {
	marked, err := fdchan.CloseOnExecFrom(3)
	if err != nil {
		return err
	}
	if marked > 0 {
		s.log.LogAttrs(ctx, slog.LevelWarn, "descriptors closed on exec",
			slog.String("rule", RuleInheritedDescriptor), slog.String("decision", "close-on-exec"),
			slog.String("slot", string(slot)), slog.Uint64("generation", generation), slog.Int("count", marked))
	}
	return nil
}

func (p *proc) closeChannels() {
	for _, c := range []*fdchan.Channel{p.control, p.accounting, p.events} {
		if c != nil {
			_ = c.Close()
		}
	}
}

func (p *proc) wait(ctx context.Context) {
	err := p.cmd.Wait()
	attrs := []slog.Attr{
		slog.String("slot", string(p.slot)), slog.Uint64("generation", p.generation),
		slog.Int("pid", p.cmd.Process.Pid), slog.String("state", p.cmd.ProcessState.String()),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	p.log.LogAttrs(ctx, slog.LevelInfo, "wb-netd exited", attrs...)
	close(p.exited)
}

// start exchanges the version records, sends the start message and the
// first policy, and waits for the policy's acknowledgment.
func (p *proc) start(link egressmsg.Link, pol *policy.Policy) error {
	if err := p.control.Send(hostversion.Record(p.s.cfg.Own), nil); err != nil {
		return fmt.Errorf("send the version record: %w", err)
	}
	if err := p.peerVersion(); err != nil {
		return err
	}
	vmName := string(p.slot)
	// No kept mapping: the payload is empty until warm start keeps one.
	err := p.sendPayload(&egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_NetdStart{
		NetdStart: egressmsg.NewNetdStart(vmName, p.generation, link),
	}}, &egressv1.NetdStartPayload{})
	if err != nil {
		return err
	}
	// wb-netd's first policy comes straight from wb-hostd: wb-proxyd
	// isn't run yet, so there is no acknowledgment of its to wait for
	// first (S07-05-hand-off, #512).
	err = p.sendPayload(&egressv1.HostdToNetd{Kind: &egressv1.HostdToNetd_PolicyChange{
		PolicyChange: &egressv1.PolicyChange{Vm: vmName, Sequence: 1},
	}}, policy.ToProto(pol))
	if err != nil {
		return err
	}
	return p.policyAck(1)
}

// recv reads one record within the ack timeout. A record with a
// descriptor is refused: wb-netd sends none at its start.
func (p *proc) recv() ([]byte, error) {
	if err := p.control.SetReadDeadline(time.Now().Add(p.s.cfg.AckTimeout)); err != nil {
		return nil, err
	}
	m, err := p.control.Recv()
	if err != nil {
		return nil, err
	}
	hasFD := m.File != nil
	_ = m.Close()
	if hasFD {
		return nil, errors.New("a record of wb-netd's start carries a descriptor")
	}
	return m.Record, p.control.SetReadDeadline(time.Time{})
}

func (p *proc) peerVersion() error {
	rec, err := p.recv()
	if err != nil {
		return fmt.Errorf("wb-netd's version record: %w", err)
	}
	_, err = hostversion.CheckRecord(p.log, p.s.cfg.Own, rec)
	return err
}

func (p *proc) policyAck(seq uint64) error {
	rec, err := p.recv()
	if err != nil {
		return fmt.Errorf("wb-netd's policy acknowledgment: %w", err)
	}
	m, ref := egressmsg.DecodeNetdToHostd(rec, false)
	if ref != nil {
		return ref
	}
	ack := m.GetPolicyAck()
	if ack.GetVm() != string(p.slot) || ack.GetSequence() != seq {
		return fmt.Errorf("wb-netd sent %v, want the acknowledgment of policy %d of %s", m, seq, p.slot)
	}
	return nil
}

// sendPayload sends m with the read end of a payload pipe, and writes
// the payload on a goroutine of its own. A write that fails leaves the
// acknowledgment out, which fails the start.
func (p *proc) sendPayload(m *egressv1.HostdToNetd, payload proto.Message) error {
	rec, err := egressmsg.Marshal(m)
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	err = p.control.Send(rec, r)
	_ = r.Close()
	if err != nil {
		_ = w.Close()
		return fmt.Errorf("send to wb-netd: %w", err)
	}
	go func() {
		defer func() { _ = w.Close() }()
		if err := egressmsg.WritePayload(w, payload); err != nil {
			p.log.LogAttrs(context.Background(), slog.LevelWarn, "wb-netd payload not written",
				slog.String("slot", string(p.slot)), slog.String("error", err.Error()))
		}
	}()
	return nil
}

// serveControl reads the control channel after the start. A record it
// refuses ends wb-netd. The channel's end is wb-netd's exit, which the
// VM manager follows, or Stop.
func (p *proc) serveControl() {
	go func() {
		for {
			m, err := p.control.Recv()
			if err != nil {
				if !p.stopping.Load() && !errors.Is(err, io.EOF) {
					p.End(RuleEnded)
				}
				return
			}
			hasFD := m.File != nil
			_ = m.Close()
			if _, ref := egressmsg.DecodeNetdToHostd(m.Record, hasFD); ref != nil {
				p.log.LogAttrs(context.Background(), slog.LevelWarn, "wb-netd record refused",
					append(ref.LogAttrs(), slog.String("slot", string(p.slot)))...)
				p.End(RuleEnded)
				return
			}
		}
	}()
}

// End ends wb-netd at once: a reader refused what it sent
// (egress.Ender, events.Ender). The VM manager stops the VM when it
// exits.
func (p *proc) End(rule string) {
	p.log.LogAttrs(context.Background(), slog.LevelWarn, "wb-netd ended",
		slog.String("rule", rule), slog.String("decision", "end-netd"),
		slog.String("slot", string(p.slot)), slog.Uint64("generation", p.generation))
	_ = p.cmd.Process.Kill()
}

// Exited implements vm.NetworkRun.
func (p *proc) Exited() <-chan struct{} { return p.exited }

// Stop implements vm.NetworkRun: it closes the control channel, which
// ends wb-netd with status 0, and kills a wb-netd that doesn't exit
// within Config.StopTimeout. Its ends of the channels close with it, so the
// readers end once they read what it left. Only then are the VM's runs
// ended (events.Hub.End) and its accounting stopped.
func (p *proc) Stop() []audit.OpenRun {
	p.stopping.Store(true)
	_ = p.control.Close()
	select {
	case <-p.exited:
	case <-time.After(p.s.cfg.StopTimeout):
		p.log.LogAttrs(context.Background(), slog.LevelWarn, "wb-netd killed",
			slog.String("rule", RuleStopTimeout), slog.String("decision", "kill"),
			slog.String("slot", string(p.slot)), slog.Uint64("generation", p.generation))
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
	<-p.receiver.Done()
	<-p.reader.Done()
	runs := p.s.cfg.Events.End(p.vm)
	p.s.cfg.Accounting.Stop(string(p.slot))
	return runs
}
