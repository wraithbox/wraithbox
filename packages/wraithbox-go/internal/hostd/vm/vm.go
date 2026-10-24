// Package vm is wb-hostd's VM manager. It keeps the state of the work
// VM and the isolated VM (S06-04-vms-and-placement), the generation each
// runs, refuses a start or a stop that its rules don't allow, and runs
// the steps before every cold start: the bundle directory, the data
// disk checks, the guest tools disk, and a new generation. It also holds
// a slot's start lock while its data disk is reset.
//
// It drives each VM run through a Provider, a new Instance per run
// (S04-04-vm-processes, "One `wb-vmd` per VM"). When the VM stops, on
// request or on its own, the manager closes that instance. It never
// starts a VM by itself: not at its own start, and not after a VM
// stopped (S04-07-supervision, "A new `wb-hostd`"). When wb-hostd exits
// it does nothing: each wb-vmd stops its VM when its lifeline closes
// (S04-07-supervision, "When `wb-hostd` stops").
//
// Every refusal is a *Refusal with its rule, logged with that rule
// (NFR06-explained-refusals).
package vm

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// Slot names a VM slot. Its value is the WB_VMD_SLOT value of the slot's
// wb-vmd (S04-04-vm-processes, "The slot").
type Slot string

// The slots. M2 has no maintenance VM and no slot lending, so at most
// two macOS VMs run, one per slot (NFR05-two-macos-vms,
// S06-07-slot-admission).
const (
	SlotWork     Slot = vmbundle.SlotWork
	SlotIsolated Slot = vmbundle.SlotIsolated
)

// Slots returns every slot, in the order a listing shows them.
func Slots() []Slot { return []Slot{SlotWork, SlotIsolated} }

func (s Slot) valid() bool { return s == SlotWork || s == SlotIsolated }

// name is the slot's bundle name without its extension, macos-work or
// macos-isolated. It is also the slot's key in state.db.
func (s Slot) name() string { return vmbundle.Name(string(s)) }

// Phase is the manager's state of a slot.
type Phase string

// The phases. A slot is stopped when wb-hostd starts.
const (
	PhaseStopped  Phase = "stopped"
	PhaseStarting Phase = "starting"
	PhaseRunning  Phase = "running"
	PhaseStopping Phase = "stopping"
)

// Status is a slot's state.
type Status struct {
	Slot  Slot
	Phase Phase
	// Generation is the generation of the current or the last run since
	// wb-hostd started, 0 before the first.
	Generation uint64
	// LastError is the last framework error of the slot's current or
	// last run, kept after the VM stopped. nil when there is none.
	LastError *vmdv1.VMError
}

// The stop deadlines, used when Config leaves them 0. `wb vm stop` asks
// the guest to shut down, and stops the VM at once when the guest
// hasn't stopped by the first deadline (S05-03-management-commands,
// "`wb vm`"). After the second, the manager closes the instance, which
// stops the VM as a lost wb-hostd does (S04-07-supervision, "When
// `wb-hostd` stops").
const (
	DefaultRequestStopTimeout = 30 * time.Second
	DefaultForceStopTimeout   = 10 * time.Second
)

// DefaultStartTimeout bounds the provider's open and its start, each,
// used when Config leaves it 0 (S05-03-management-commands, "`wb vm`").
// wb-vmd answers once the framework has started the VM. A cold boot to
// wb-guestd's listener takes 10.9 s at the median and 13.5 s at p95
// (S06-06-devices-and-warm-start, "Warm start"), so a start without an
// answer after 60 s is a wb-vmd that hangs. The start is then refused
// with vm-start-timeout and the instance closed.
const DefaultStartTimeout = 60 * time.Second

// Config is what the manager runs with.
type Config struct {
	// DataDir is <data>. Each slot's bundle is
	// <data>/vms/macos-<slot>.noindex (S04-00-architecture, "Host
	// state").
	DataDir string
	// ToolsImage is the installed bundle's guest tools disk,
	// Contents/Resources/wb-guest-tools.img on macOS (S17-01-bundle).
	ToolsImage string
	// Resources has the CPU count and memory of each VM.
	Resources config.Config
	// Generations hands out generations. Only nil in refusing mode.
	Generations Generations
	Provider    Provider
	Cloner      Cloner
	// DataDisks runs the data disk checks before each start, records the
	// first start, and resets a disk (S06-13-data-disk-format). Only nil in
	// refusing mode.
	DataDisks DataDisks
	// Sessions lists the slot's sessions that are starting, running,
	// paused or ending. nil means none. Stop calls it before the slot
	// moves to stopping, so session admission must refuse a VM whose
	// Status isn't running: then no session starts in a VM that stops.
	Sessions func(Slot) []Session
	// OnRunning, when set, is called once for each run that reaches
	// running, with the slot, the run's generation, and a channel that is
	// closed once the run has ended: its instance closed and the slot
	// stopped. wb-hostd's guest link follows each run through it
	// (internal/hostd/guestconn). It is called without the manager's
	// lock, and must not block.
	OnRunning func(slot Slot, generation uint64, ended <-chan struct{})
	// Refusing is set in refusing mode, and every request is then refused
	// with its rule (S17-05-upgrades-and-migrations, "Refusing mode").
	Refusing *startup.Refusing
	// StartTimeout bounds the provider's start. 0 takes the default.
	StartTimeout time.Duration
	// RequestStopTimeout and ForceStopTimeout are the stop deadlines. 0
	// takes the default.
	RequestStopTimeout time.Duration
	ForceStopTimeout   time.Duration
	// Logger gets the decisions. nil takes slog.Default().
	Logger *slog.Logger
}

func (c *Config) check() error {
	var errs []error
	if !filepath.IsAbs(c.DataDir) {
		errs = append(errs, fmt.Errorf("vm: the data directory %q isn't an absolute path", c.DataDir))
	}
	if !filepath.IsAbs(c.ToolsImage) {
		errs = append(errs, fmt.Errorf("vm: the guest tools disk %q isn't an absolute path", c.ToolsImage))
	}
	if c.Provider == nil || c.Cloner == nil {
		errs = append(errs, errors.New("vm: a provider and a cloner are required"))
	}
	if (c.Generations == nil || c.DataDisks == nil) && c.Refusing == nil {
		errs = append(errs, errors.New("vm: generations and data disks are required outside refusing mode"))
	}
	return errors.Join(errs...)
}

// Manager runs the VMs of every slot. Its methods are safe for
// concurrent use.
type Manager struct {
	cfg Config
	log *slog.Logger

	mu    sync.Mutex
	slots map[Slot]*slotRecord

	// runs counts the goroutines that follow a run and end it.
	runs sync.WaitGroup
}

// Wait returns once every run has ended and its end is logged. A run
// ends when its instance's States closes, so wb-hostd calls Wait after
// its provider has closed every instance, and before it closes the log
// that gets the records of the runs' ends.
func (m *Manager) Wait() { m.runs.Wait() }

// slotRecord is a slot's state, guarded by Manager.mu.
type slotRecord struct {
	phase      Phase
	generation uint64
	lastErr    *vmdv1.VMError
	// run is the current run, nil while the slot is stopped.
	run *run
	// resetting: the slot's data disk is being reset, so a start is
	// refused.
	resetting bool
}

// New returns a manager with every slot stopped. It starts no VM.
func New(cfg Config) (*Manager, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	if cfg.RequestStopTimeout <= 0 {
		cfg.RequestStopTimeout = DefaultRequestStopTimeout
	}
	if cfg.ForceStopTimeout <= 0 {
		cfg.ForceStopTimeout = DefaultForceStopTimeout
	}
	m := &Manager{cfg: cfg, log: cfg.Logger, slots: map[Slot]*slotRecord{}}
	if m.log == nil {
		m.log = slog.Default()
	}
	for _, s := range Slots() {
		m.slots[s] = &slotRecord{phase: PhaseStopped}
	}
	return m, nil
}

// Status returns slot's state. An unknown slot reads as stopped.
func (m *Manager) Status(slot Slot) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(slot)
}

// Statuses returns the state of every slot, in the order of Slots.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.slots))
	for _, s := range Slots() {
		out = append(out, m.statusLocked(s))
	}
	return out
}

func (m *Manager) statusLocked(slot Slot) Status {
	rec, ok := m.slots[slot]
	if !ok {
		return Status{Slot: slot, Phase: PhaseStopped}
	}
	return Status{Slot: slot, Phase: rec.phase, Generation: rec.generation, LastError: cloneVMError(rec.lastErr)}
}

// runningLocked lists the slots that aren't stopped, but for except.
func (m *Manager) runningLocked(except Slot) []Status {
	var out []Status
	for _, s := range Slots() {
		if s != except && m.slots[s].phase != PhaseStopped {
			out = append(out, m.statusLocked(s))
		}
	}
	return out
}

// checkRequest refuses every request in refusing mode, and a request
// for a slot that doesn't exist.
func (m *Manager) checkRequest(slot Slot) *Refusal {
	if r := m.cfg.Refusing; r != nil {
		return &Refusal{
			Rule: Rule(r.Rule), Slot: slot, Phase: PhaseStopped, err: r,
			Message: "wb-hostd is in refusing mode, and runs no VM: " + r.Detail,
		}
	}
	if !slot.valid() {
		return &Refusal{
			Rule: RuleSlotUnknown, Slot: slot,
			Message: fmt.Sprintf("there is no %q VM: the VMs are %s and %s", slot, SlotWork, SlotIsolated),
		}
	}
	return nil
}

// bundlePath is the slot's own bundle. A slot uses its bundle, never
// another's (SEC08-proj-isolation).
func (m *Manager) bundlePath(slot Slot) string {
	return vmbundle.Path(m.cfg.DataDir, string(slot))
}

// phaseRefusal refuses a request that needs another phase than rec's.
func phaseRefusal(slot Slot, rec *slotRecord, op string) *Refusal {
	r := &Refusal{Slot: slot, Phase: rec.phase, Generation: rec.generation}
	switch rec.phase {
	case PhaseStopped:
		r.Rule = RuleNotRunning
		r.Message = fmt.Sprintf("can't %s the %s VM: it isn't running", op, slot)
	case PhaseStarting:
		r.Rule = RuleStarting
		r.Message = fmt.Sprintf("can't %s the %s VM: it is starting. Try again once it runs", op, slot)
	case PhaseStopping:
		r.Rule = RuleStopping
		r.Message = fmt.Sprintf("can't %s the %s VM: it is stopping", op, slot)
	case PhaseRunning:
		r.Rule = RuleRunning
		r.Message = fmt.Sprintf("can't %s the %s VM: it is running (generation %d)", op, slot, rec.generation)
	}
	return r
}
