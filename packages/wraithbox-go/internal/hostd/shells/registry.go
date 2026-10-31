package shells

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// MaxPerVM is the most open shells in one VM: the 64 sessions of
// S06-11-reconnect-and-recovery ("`wb-hostd` runs at most 64 sessions in
// one VM"), which a debug shell counts against
// (S06-07-slot-admission; SEC13-bounded-resources).
const MaxPerVM = 64

// The rules of the registry.
const (
	// RuleSessionCap: the VM already has MaxPerVM open shells.
	RuleSessionCap = "vm-session-cap"
	// RuleIDTaken: the shell id is already an open shell's.
	RuleIDTaken = "shell-id-taken"
	// RuleKill: the lock and kill of a project user after its last shell,
	// or after a lost one, sent or not.
	RuleKill = "shell-user-kill"
	// RuleIsolatedTaken: another project has a shell in the isolated VM.
	RuleIsolatedTaken = "isolated-slot-taken"
)

// ErrIDTaken is returned by Register for an id an open shell has.
var ErrIDTaken = errors.New("shells: the shell id is in use")

// CapRefusal is the refusal of a shell over MaxPerVM.
type CapRefusal struct {
	Slot vm.Slot
}

func (r *CapRefusal) Error() string {
	return fmt.Sprintf("the %s VM already has %d open shells, the most one VM holds. Close one and try again.", r.Slot, MaxPerVM)
}

// TakenRefusal is the refusal of a shell in the isolated VM while
// another project has one there. OtherProjectID is that project's id.
type TakenRefusal struct {
	OtherProjectID string
}

func (r *TakenRefusal) Error() string {
	return "shells: another project has a shell in the isolated VM: " + r.OtherProjectID
}

// Killer sends the lock and kill loop of a project user
// (S13-01-process-hardening, "When a project's last session ends").
// *guestconn.Links implements it.
type Killer interface {
	KillUser(ctx context.Context, slot vm.Slot, generation uint64, u state.ProjectUser) error
}

// Registry holds the open debug shells of every VM, in memory only: VMs
// stop with wb-hostd, so no shell outlives it (S06-08-projects-and-sessions,
// "Debug shells in V1-M2"). Its methods are safe for concurrent use.
type Registry struct {
	killer Killer
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	kills  sync.WaitGroup

	mu    sync.Mutex
	slots map[vm.Slot]*slotShells
	// closed is set by Close, after which no kill starts, so the kills'
	// WaitGroup gets no Add after its Wait.
	closed bool
}

// slotShells is the registry's state of one VM.
type slotShells struct {
	shells map[*Shell]struct{}
	// generation is the run that lost and killing are of.
	generation uint64
	// lost holds the users of lost shells by project id, whose kill waits
	// for the next connection of generation (S13-01-process-hardening,
	// "When the link drops").
	lost map[string]state.ProjectUser
	// killing holds the projects whose kill the registry is beginning, so
	// a new shell of the project waits for it.
	killing map[string]bool
	// skipped holds the users of ended shells whose kill End skipped for
	// another shell of the project, which may not start. Release of the
	// project's last shell sends it.
	skipped map[string]state.ProjectUser
	// links counts the connections of generation (OnLinked), so End and a
	// failed kill can tell a link that came up again since.
	links uint64
}

// NewRegistry returns an empty registry that sends kills through k.
func NewRegistry(k Killer, log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{killer: k, log: log, ctx: ctx, cancel: cancel, slots: map[vm.Slot]*slotShells{}}
}

// Close ends the kills the registry sends, and waits for them. No kill
// starts after it.
func (g *Registry) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.cancel()
	g.kills.Wait()
}

// Shell is one open debug shell.
type Shell struct {
	g           *Registry
	ID          string
	Slot        vm.Slot
	ProjectID   string
	PID         int
	Interactive bool

	// Set by Bind, and then fixed. link is the slot's connection count
	// then.
	generation uint64
	user       state.ProjectUser
	bound      bool
	link       uint64

	// state is guarded by the registry's mutex.
	state vm.SessionState
}

// Register records a starting shell with id in slot, before its VM's
// phase is checked, so a `wb vm stop` that reads the shells after it
// lists it (vm.Manager.Stop). It refuses the shell over MaxPerVM with a
// *CapRefusal, an id an open shell has with ErrIDTaken, and a shell in
// the isolated VM while another project has one there with a
// *TakenRefusal.
func (g *Registry) Register(id string, slot vm.Slot, projectID string, pid int, interactive bool) (*Shell, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.slotLocked(slot)
	if len(s.shells) >= MaxPerVM {
		return nil, &CapRefusal{Slot: slot}
	}
	for sh := range s.shells {
		if sh.ID == id {
			return nil, ErrIDTaken
		}
		// An isolated project gets the isolated VM to itself. No project
		// is marked shared before its settings exist, so each one is
		// alone there (S06-04-vms-and-placement, "Isolated slot to
		// itself"; SEC08-proj-isolation).
		if slot == vm.SlotIsolated && sh.ProjectID != projectID {
			return nil, &TakenRefusal{OtherProjectID: sh.ProjectID}
		}
	}
	sh := &Shell{g: g, ID: id, Slot: slot, ProjectID: projectID, PID: pid, Interactive: interactive, state: vm.SessionStarting}
	s.shells[sh] = struct{}{}
	return sh, nil
}

func (g *Registry) slotLocked(slot vm.Slot) *slotShells {
	s := g.slots[slot]
	if s == nil {
		s = &slotShells{
			shells: map[*Shell]struct{}{}, lost: map[string]state.ProjectUser{},
			killing: map[string]bool{}, skipped: map[string]state.ProjectUser{},
		}
		g.slots[slot] = s
	}
	return s
}

// seeLocked moves s to generation when it is newer: the lost users and
// kills of an older run went with its VM.
func (s *slotShells) seeLocked(generation uint64) {
	if generation > s.generation {
		s.generation = generation
		s.links = 0
		clear(s.lost)
		clear(s.killing)
		clear(s.skipped)
	}
}

// Killing reports whether the registry is beginning a kill of
// projectID's user in slot's run of generation. A shell of the project
// waits until it isn't.
func (g *Registry) Killing(slot vm.Slot, generation uint64, projectID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.slotLocked(slot)
	return s.generation == generation && s.killing[projectID]
}

// Sessions lists slot's open shells for vm.Config.Sessions, so `wb vm
// stop` is refused while one is open (S06-09-session-lifecycle, "Idle
// suspend").
func (g *Registry) Sessions(slot vm.Slot) []vm.Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []vm.Session
	for sh := range g.slotLocked(slot).shells {
		out = append(out, vm.Session{ID: sh.ID, State: sh.state, PID: sh.PID})
	}
	return out
}

// Bind records the run of generation the shell starts in, and its
// project user.
func (sh *Shell) Bind(generation uint64, u state.ProjectUser) {
	sh.g.mu.Lock()
	defer sh.g.mu.Unlock()
	s := sh.g.slotLocked(sh.Slot)
	s.seeLocked(generation)
	sh.generation, sh.user, sh.bound, sh.link = generation, u, true, s.links
}

// Generation returns the generation Bind recorded.
func (sh *Shell) Generation() uint64 {
	sh.g.mu.Lock()
	defer sh.g.mu.Unlock()
	return sh.generation
}

// SetState records the shell's state, which `wb vm stop` names.
func (sh *Shell) SetState(st vm.SessionState) {
	sh.g.mu.Lock()
	defer sh.g.mu.Unlock()
	sh.state = st
}

// Release removes a shell that never started in the guest. It sends no
// kill for itself: the project user ran nothing for it. When an ended
// shell's kill was skipped for this one, and no other shell of the
// project is open, Release sends that kill (S13-01-process-hardening,
// "When a project's last session ends").
func (sh *Shell) Release() {
	g := sh.g
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.slotLocked(sh.Slot)
	delete(s.shells, sh)
	u, ok := s.skipped[sh.ProjectID]
	if !ok || s.hasProjectLocked(sh.ProjectID) {
		return
	}
	g.startKillLocked(s, sh.Slot, s.generation, u, "the project's last shell ended, and a shell that waited for it didn't start")
}

// End removes a shell that started in the guest, or that may have: its
// user may be unlocked. A lost one records its user, whose kill waits for
// the next connection, or runs now when a connection came up since the
// shell started. Otherwise, when the VM has no other shell of the
// project, End has wb-guestd lock the user and run the kill loop
// (S13-01-process-hardening, "When a project's last session ends"), and
// logs the result. The kill runs on after End returns, and a later shell
// of the project waits for it (Killing).
func (sh *Shell) End(ctx context.Context, lost bool) {
	g := sh.g
	g.mu.Lock()
	s := g.slotLocked(sh.Slot)
	delete(s.shells, sh)
	if !sh.bound || sh.generation != s.generation {
		g.mu.Unlock()
		return
	}
	relinked := s.links > sh.link
	if lost && !relinked {
		s.lost[sh.ProjectID] = sh.user
		g.mu.Unlock()
		g.logKill(ctx, sh.Slot, sh.generation, sh.user, "wait", "the shell was lost, so the kill waits for the next connection")
		return
	}
	if s.hasProjectLocked(sh.ProjectID) {
		if lost {
			s.lost[sh.ProjectID] = sh.user
		} else {
			s.skipped[sh.ProjectID] = sh.user
		}
		g.mu.Unlock()
		g.logKill(ctx, sh.Slot, sh.generation, sh.user, "skip", "another shell of the project is open")
		return
	}
	why := "the project's last shell ended"
	if lost {
		why = "the shell was lost, and the link is up again"
	}
	g.startKillLocked(s, sh.Slot, sh.generation, sh.user, why)
	g.mu.Unlock()
}

// startKillLocked begins the kill of u, which covers a lost or skipped
// kill of its project too. It starts none after Close.
func (g *Registry) startKillLocked(s *slotShells, slot vm.Slot, generation uint64, u state.ProjectUser, why string) {
	if g.closed {
		return
	}
	delete(s.lost, u.ProjectID)
	delete(s.skipped, u.ProjectID)
	s.killing[u.ProjectID] = true
	link := s.links
	g.kills.Go(func() { g.kill(slot, generation, link, u, why) })
}

func (s *slotShells) hasProjectLocked(projectID string) bool {
	for other := range s.shells {
		if other.ProjectID == projectID {
			return true
		}
	}
	return false
}

// OnLinked is guestconn's linked hook. On each connection of slot's run
// of generation, it begins the kill of each user of a lost shell whose
// project has no open shell (S13-01-process-hardening, "When the link
// drops"), and leaves every other user alone. It doesn't block.
func (g *Registry) OnLinked(slot vm.Slot, generation uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.slotLocked(slot)
	s.seeLocked(generation)
	if s.generation != generation {
		return
	}
	s.links++
	for projectID, u := range s.lost {
		if s.hasProjectLocked(projectID) {
			continue
		}
		g.startKillLocked(s, slot, generation, u, "a shell of the project was lost under the last link")
	}
}

// kill sends the kill of u, which began under the slot's connection
// link, and then lets shells of the project start. A kill that didn't
// reach the guest (guestconn.ErrKillNotSent) begins again at once when a
// connection came up since, and otherwise waits as lost for the next.
func (g *Registry) kill(slot vm.Slot, generation, link uint64, u state.ProjectUser, why string) {
	err := g.killer.KillUser(g.ctx, slot, generation, u)
	g.mu.Lock()
	s := g.slotLocked(slot)
	if s.generation == generation {
		delete(s.killing, u.ProjectID)
		if errors.Is(err, guestconn.ErrKillNotSent) && g.ctx.Err() == nil {
			s.lost[u.ProjectID] = u
			if s.links > link && !s.hasProjectLocked(u.ProjectID) {
				g.startKillLocked(s, slot, generation, u, why+", sent again on the new link")
			}
		}
	}
	g.mu.Unlock()
	if err != nil {
		g.logKill(g.ctx, slot, generation, u, "failed", why+": "+err.Error())
		return
	}
	g.logKill(g.ctx, slot, generation, u, "send", why)
}

func (g *Registry) logKill(ctx context.Context, slot vm.Slot, generation uint64, u state.ProjectUser, decision, why string) {
	level := slog.LevelInfo
	if decision == "failed" {
		level = slog.LevelWarn
	}
	g.log.LogAttrs(ctx, level, "shell user kill",
		slog.String("rule", RuleKill), slog.String("decision", decision),
		slog.String("slot", string(slot)), slog.Uint64("generation", generation),
		slog.String("project", u.ProjectID), slog.Uint64("uid", uint64(u.UID)), slog.String("reason", why))
}
