// Package netdproc starts and stops each VM run's wb-netd
// (S04-09-netd-start): it spawns the program with
// the start interface of cmd/wb-netd, sends the start message and the
// first policy on the control channel, reads the accounting and event
// channels, and at the stop closes the control channel, waits for the
// exit and reads what is left. It implements vm.Network.
//
// There are no restarts: the VM manager stops the VM when wb-netd exits
// (vm.RuleNetdExited).
package netdproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/egress"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/events"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// Process is wb-netd's name in the logs and in its log file.
const Process = "wb-netd"

// The rules netdproc logs. vm.RuleNetdStartFailed is the start's.
const (
	// RuleEnded: a channel reader refused what wb-netd sent, or the
	// control channel failed, and wb-netd was ended. The VM manager then
	// stops the VM (vm.RuleNetdExited).
	RuleEnded = "netd-ended"
	// RuleStopTimeout: wb-netd didn't exit within Config.StopTimeout after its
	// control channel closed, and was killed.
	RuleStopTimeout = "netd-stop-timeout"
)

// DefaultStopTimeout is Config.StopTimeout when it is zero. It outlasts
// wb-netd's own flush of its event channel.
const DefaultStopTimeout = 10 * time.Second

// The addressing of every VM's link (S04-09-netd-start, "Start
// message and first policy").
// Each VM has its own link, so both use the same subnet.
var (
	linkSubnet  = netip.MustParsePrefix("192.168.127.0/24")
	linkGateway = netip.MustParseAddr("192.168.127.1")
	linkLeased  = netip.MustParseAddr("192.168.127.2")
)

// Config is what New needs.
type Config struct {
	// Program is the installed bundle's wb-netd.
	Program string
	// Own is wb-hostd's build version.
	Own string
	// Logs returns the writer of a process's log file
	// (logpipe.Files.Writer).
	Logs func(process string) io.Writer
	// Log takes netdproc's decisions. nil is slog.Default().
	Log *slog.Logger
	// Accounting keeps the accounting records of every VM.
	Accounting *egress.State
	// Events reads the event channels into the audit log.
	Events *events.Hub
	// Policy returns the effective policy of a start
	// (policyfile.Static).
	Policy func(ctx context.Context) (*policy.Policy, error)
	// AckTimeout bounds the wait for wb-netd's version record and its
	// acknowledgment of the first policy. Zero is egress.AckTimeout.
	AckTimeout time.Duration
	// StopTimeout bounds the wait for wb-netd's exit after its control
	// channel closed. Zero is DefaultStopTimeout.
	StopTimeout time.Duration
}

// Starter starts each run's wb-netd.
type Starter struct {
	cfg Config
	log *slog.Logger
}

// New returns a Starter, or an error for a Config it can't use.
func New(cfg Config) (*Starter, error) {
	var errs []error
	if !filepath.IsAbs(cfg.Program) {
		errs = append(errs, fmt.Errorf("netdproc: the program %q isn't an absolute path", cfg.Program))
	}
	if cfg.Logs == nil || cfg.Accounting == nil || cfg.Events == nil || cfg.Policy == nil {
		errs = append(errs, errors.New("netdproc: Logs, Accounting, Events and Policy are required"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = egress.AckTimeout
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = DefaultStopTimeout
	}
	return &Starter{cfg: cfg, log: cfg.Log}, nil
}

// childTransport is a packet transport that hands its descriptor to a
// child: *vmhandoff.Transport.
type childTransport interface {
	StartWith(cmd *exec.Cmd, index int) error
}

// Prepare implements vm.Network. It refuses a MAC address that isn't a
// unicast Ethernet address and a policy that doesn't load, before the
// VM starts, and logs every refusal with vm.RuleNetdStartFailed.
func (s *Starter) Prepare(ctx context.Context, slot vm.Slot, generation uint64, mac []byte) (vm.NetworkStart, error) {
	link, err := Link(mac)
	if err != nil {
		return nil, s.refuse(ctx, slot, generation, "link", err)
	}
	pol, err := s.cfg.Policy(ctx)
	if err != nil {
		return nil, s.refuse(ctx, slot, generation, "policy", err)
	}
	return &prepared{s: s, slot: slot, generation: generation, link: link, pol: pol}, nil
}

// prepared is a start with its link and first policy.
type prepared struct {
	s          *Starter
	slot       vm.Slot
	generation uint64
	link       egressmsg.Link
	pol        *policy.Policy
}

// Start implements vm.NetworkStart. It refuses a transport that can't
// hand its descriptor to a child, and logs every refusal with
// vm.RuleNetdStartFailed.
func (ps *prepared) Start(ctx context.Context, t vm.PacketTransport) (vm.NetworkRun, error) {
	s, slot, generation := ps.s, ps.slot, ps.generation
	ct, ok := t.(childTransport)
	if !ok {
		return nil, s.refuse(ctx, slot, generation, "transport", errors.New("the packet transport can't hand its descriptor to a child"))
	}
	p, err := s.spawn(ctx, slot, generation, ct)
	if err != nil {
		return nil, s.refuse(ctx, slot, generation, "spawn", err)
	}
	if err := p.start(ps.link, ps.pol); err != nil {
		_ = p.Stop()
		return nil, s.refuse(ctx, slot, generation, "start-message", err)
	}
	p.serveControl()
	s.log.LogAttrs(ctx, slog.LevelInfo, "wb-netd started",
		slog.String("decision", "accept"), slog.String("slot", string(slot)),
		slog.Uint64("generation", generation), slog.Int("pid", p.cmd.Process.Pid))
	return p, nil
}

func (s *Starter) refuse(ctx context.Context, slot vm.Slot, generation uint64, step string, err error) error {
	s.log.LogAttrs(ctx, slog.LevelError, "wb-netd not started",
		slog.String("rule", string(vm.RuleNetdStartFailed)), slog.String("decision", "refuse"),
		slog.String("slot", string(slot)), slog.Uint64("generation", generation),
		slog.String("step", step), slog.String("reason", err.Error()))
	return fmt.Errorf("netdproc: %s: %w", step, err)
}

// Link returns the addressing of the link of a guest with MAC address
// mac. The gateway's MAC is the guest's with its last byte inverted, so
// it is another unicast address of the same locally administered kind.
func Link(mac []byte) (egressmsg.Link, error) {
	if len(mac) != 6 || mac[0]&1 != 0 {
		return egressmsg.Link{}, fmt.Errorf("the guest's MAC address %x isn't a unicast Ethernet address", mac)
	}
	guest := net.HardwareAddr(append([]byte(nil), mac...))
	gateway := net.HardwareAddr(append([]byte(nil), mac...))
	gateway[5] ^= 0xff
	return egressmsg.Link{
		GuestMAC: guest, GatewayMAC: gateway,
		Subnet: linkSubnet, Gateway: linkGateway, Leased: linkLeased,
	}, nil
}
