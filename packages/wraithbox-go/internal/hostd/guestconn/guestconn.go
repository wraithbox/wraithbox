// Package guestconn is wb-hostd's set of connections to each VM's guest:
// which guest ports it connects to, how many connections it holds, and
// the control link to wb-guestd that it keeps for each running VM and
// gets back after a drop (S04-02-vm-provider, "The host opens every
// connection", "Caps and closing"; S06-11-reconnect-and-recovery,
// "Reconnect").
//
// It is a host-guest socket handler, so everything that arrives on a
// connection is guest input (SEC11-root-gains-nothing):
//
//   - A connection is asked for only on a port of a fixed table, all
//     below 1024, and goes to the code that asked for that port. Nothing
//     the guest sends picks the handler (SEC08-proj-isolation,
//     S04-02-vm-provider, "Guest ports below 1024").
//   - Live connections per VM and per project are capped, and so are the
//     connections opened per VM in a minute. Over a cap a connection is
//     refused at once, never queued (SEC13-bounded-resources).
//   - The control link takes only wb-hostd's own build version
//     (internal/guestlink, S04-08-records-and-bounds, "With
//     `wb-guestd`").
//   - Every refusal is logged with its rule, at most once per rule per VM
//     in a window (internal/ratelog).
//
// A VM's connections all close when its run ends: when the VM stops, and
// so before its generation changes.
package guestconn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// VMs opens connections to a running guest. *vm.Manager implements it:
// it refuses a generation that isn't the slot's current one, and a guest
// port of 1024 or more.
type VMs interface {
	ConnectGuest(ctx context.Context, slot vm.Slot, generation uint64, port uint32) (net.Conn, error)
}

// Clock is the time of the reconnect waits and the clock schedule.
// Tests pass a fake.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// TimeHook sends the host's time to the guest of a linked VM
// (S06-06-devices-and-warm-start, "When the host sends the time"). The
// link calls it after each accepted hello, and every TimeInterval while
// the link is up. It must return: the link doesn't watch for a drop
// while it runs. wb-hostd never reads a time from the guest.
type TimeHook func(ctx context.Context, slot vm.Slot, generation uint64, link *guestlink.Link)

// Config is what Links runs with.
type Config struct {
	VMs VMs
	// Version is wb-hostd's own build version, normally version.Build().
	// "" is an unstamped build, which matches no guest.
	Version string
	// TimeHook, when set, is called on the clock schedule. nil does
	// nothing.
	TimeHook TimeHook
	// Clock is nil for the real clock.
	Clock Clock
	// Logger gets the decisions. nil takes slog.Default().
	Logger *slog.Logger
}

// Links keeps the connections to every VM's guest. Its methods are safe
// for concurrent use.
type Links struct {
	cfg   Config
	clock Clock
	log   *slog.Logger

	mu     sync.Mutex
	vms    map[vm.Slot]*vmConns
	status map[vm.Slot]Status
	closed bool

	done      chan struct{}
	closeOnce sync.Once
	runs      sync.WaitGroup
}

// New returns Links that follow no VM yet.
func New(cfg Config) (*Links, error) {
	if cfg.VMs == nil {
		return nil, errors.New("guestconn: a VM manager is required")
	}
	ls := &Links{
		cfg: cfg, clock: cfg.Clock, log: cfg.Logger,
		vms: map[vm.Slot]*vmConns{}, status: map[vm.Slot]Status{},
		done: make(chan struct{}),
	}
	if ls.clock == nil {
		ls.clock = realClock{}
	}
	if ls.log == nil {
		ls.log = slog.Default()
	}
	return ls, nil
}

// vm returns slot's connection set, made on first use.
func (ls *Links) vm(slot vm.Slot) *vmConns {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	vc, ok := ls.vms[slot]
	if !ok {
		vc = newVMConns(slot, ls.log, ls.clock.Now)
		ls.vms[slot] = vc
	}
	return vc
}

// Run starts the control link of slot's run of generation, and keeps it
// until ended is closed or Close is called. vm.Config.OnRunning calls it
// for each run that reaches running. It doesn't block.
func (ls *Links) Run(slot vm.Slot, generation uint64, ended <-chan struct{}) {
	ls.mu.Lock()
	if ls.closed || ls.status[slot].Generation >= generation {
		ls.mu.Unlock()
		return
	}
	ls.status[slot] = Status{Slot: slot, Generation: generation, State: StateConnecting}
	ls.runs.Add(1)
	ls.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	r := &run{ls: ls, vc: ls.vm(slot), slot: slot, generation: generation, ctx: ctx}
	go func() {
		select {
		case <-ended:
		case <-ls.done:
		}
		cancel()
	}()
	go func() {
		defer ls.runs.Done()
		r.loop()
		cancel()
		r.vc.closeGeneration(generation)
		ls.setStatus(slot, generation, func(s *Status) { s.State = StateDown })
		r.vc.log.LogAttrs(context.Background(), slog.LevelInfo, "guest link ended",
			slog.String("decision", "close"), slog.Uint64("generation", generation))
	}()
}

// Close ends every link, closes every connection, and waits until every
// link's loop has returned.
func (ls *Links) Close() {
	ls.closeOnce.Do(func() {
		ls.mu.Lock()
		ls.closed = true
		ls.mu.Unlock()
		close(ls.done)
	})
	ls.runs.Wait()
	ls.mu.Lock()
	vms := make([]*vmConns, 0, len(ls.vms))
	for _, vc := range ls.vms {
		vms = append(vms, vc)
	}
	ls.mu.Unlock()
	for _, vc := range vms {
		vc.closeAll()
	}
}

// State is the state of a VM's control link.
type State string

// The states.
const (
	// StateDown: no run is followed, or it ended.
	StateDown State = "down"
	// StateConnecting: the link isn't up yet, or dropped, and the next
	// try is pending.
	StateConnecting State = "connecting"
	// StateLinked: the hello was accepted, and the link is up.
	StateLinked State = "linked"
	// StateRefused: the last hello was refused. The link tries again.
	StateRefused State = "refused"
)

// Status is the state of a slot's control link.
type Status struct {
	Slot       vm.Slot
	Generation uint64
	State      State
	// Refusal is the last refused hello of this generation, kept until a
	// hello is accepted. nil when there is none.
	Refusal *HelloRefusal
}

// HelloRefusal is a refused hello, for `wb vm status` and a session start
// to explain (NFR06-explained-refusals).
type HelloRefusal struct {
	Rule string
	// OwnVersion and GuestVersion are the two versions as version.Label
	// shows them: the guest's escaped and cut to 64 bytes. GuestVersion is
	// "" when the refusal wasn't about the version.
	OwnVersion   string
	GuestVersion string
	// Message says what was refused, and what to do.
	Message string
}

// Status returns slot's link state.
func (ls *Links) Status(slot vm.Slot) Status {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	s, ok := ls.status[slot]
	if !ok {
		return Status{Slot: slot, State: StateDown}
	}
	if s.Refusal != nil {
		r := *s.Refusal
		s.Refusal = &r
	}
	return s
}

// setStatus changes slot's status when it is still of generation.
func (ls *Links) setStatus(slot vm.Slot, generation uint64, change func(*Status)) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	s := ls.status[slot]
	if s.Generation != generation {
		return
	}
	change(&s)
	ls.status[slot] = s
}

// helloRefusal describes a refused hello of slot's guest.
func helloRefusal(slot vm.Slot, own string, err error) *HelloRefusal {
	out := &HelloRefusal{Rule: guestlink.RuleInvalid, OwnVersion: version.Label(own)}
	if r, ok := errors.AsType[*guestlink.Refusal](err); ok {
		out.Rule = r.Rule
	}
	vr, ok := errors.AsType[*version.Refusal](err)
	if !ok {
		out.Message = fmt.Sprintf("wb-guestd in the %s VM was refused (%s). Stop and start the VM.", slot, out.Rule)
		return out
	}
	out.GuestVersion = vr.PeerLabel()
	out.Message = fmt.Sprintf("wb-guestd in the %s VM is version %s, and wb-hostd is version %s (%s). "+
		"Stop and start the VM. If that doesn't help, rebuild the image with `wb image build`.",
		slot, out.GuestVersion, out.OwnVersion, out.Rule)
	return out
}

// newVMLog is the log of one VM's links: every record names the slot,
// and a refusal is written at most once per rule per ratelog.Window.
func newVMLog(base *slog.Logger, slot vm.Slot, now func() time.Time) *slog.Logger {
	return ratelog.NewAt(base.With(slog.String("slot", string(slot))), ratelog.Window, now)
}
