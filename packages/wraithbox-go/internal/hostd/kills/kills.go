// Package kills is wb-hostd's record of the kills of project users it
// sends to wb-guestd, and of their results (S06-10-leftover-processes).
//
// Every result is guest input (SEC11-root-gains-nothing). The tracker
// takes one only for a kill ID it sent, for that project, in that VM and
// VM generation, before the kill's deadline. A kill without an accepted
// clean result by then counts as processes left, so a faked count, a
// withheld result or a late one only makes the host stricter. A result
// of processes left refuses every new session in the VM generation and
// every save of it (SEC08-proj-isolation), until a cold boot starts a new
// generation.
//
// Pending kills are kept in memory only. A wb-hostd exit stops every VM,
// which closes them either way (S06-10-leftover-processes, "Failing
// closed").
package kills

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// Deadline is how long a kill waits for its result, in host time awake
// (S06-09-session-lifecycle, "Deadlines").
const Deadline = 7 * time.Minute

// The rules of the tracker's decisions. Each one is logged with its
// decision.
const (
	// RuleSent: wb-hostd sent a kill, and it is pending.
	RuleSent = "kill-sent"
	// RuleClean: a result of no processes left was accepted.
	RuleClean = "kill-clean"
	// RuleProcessesLeft: a result of processes left was accepted.
	RuleProcessesLeft = "kill-processes-left"
	// RuleDeadline: no result was accepted by the kill's deadline, which
	// counts as processes left.
	RuleDeadline = "kill-deadline"
	// RuleColdStop: the kill's VM generation ended in a cold stop before
	// the deadline, which closes the kill.
	RuleColdStop = "kill-closed-cold-stop"
	// RuleProjectForm: the result's project ID doesn't pass the format
	// check of S05-04-naming.
	RuleProjectForm = "kill-result-project-form"
	// RuleUnknownKill: the result names a kill ID wb-hostd didn't send
	// for that project in this VM and VM generation.
	RuleUnknownKill = "kill-result-unknown"
	// RuleLate: the result is for a kill whose deadline passed, or that a
	// cold stop closed.
	RuleLate = "kill-result-late"
	// RuleAgain: a result for a kill whose result was accepted before.
	RuleAgain = "kill-result-again"
	// RuleListTooMany: the reconnect list holds more cleanup records than
	// project users in the VM.
	RuleListTooMany = "cleanup-list-too-many"
	// RuleListDuplicate: the reconnect list holds two cleanup records
	// with one kill ID.
	RuleListDuplicate = "cleanup-list-duplicate"
	// RuleVMProcessesLeft: a result of processes left in this VM
	// generation refuses new sessions, learn mode and saves.
	RuleVMProcessesLeft = "vm-processes-left"
	// RuleKillPending: a kill in this VM generation is pending, which
	// refuses a save, and a session of its project.
	RuleKillPending = "vm-kill-pending"
)

// Clock is the time of the deadlines, which count host time awake.
// Tests pass a fake.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f in its own goroutine once d has passed.
	AfterFunc(d time.Duration, f func())
}

// realClock is Go's monotonic clock. On macOS it follows
// mach_absolute_time, which doesn't advance while the host sleeps.
type realClock struct{}

func (realClock) Now() time.Time                      { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) { time.AfterFunc(d, f) }

// Config is what a Tracker runs with.
type Config struct {
	// Clock is nil for the real clock.
	Clock Clock
	// Logger gets the decisions. nil takes slog.Default().
	Logger *slog.Logger
}

// status is where a kill stands.
type status int

const (
	pending status = iota
	accepted
	expired
	closed
)

// kill is one kill wb-hostd sent.
type kill struct {
	id        uint64
	projectID string
	deadline  time.Time
	status    status
}

// Left is a result of processes left: an accepted one, or a kill whose
// deadline passed.
type Left struct {
	KillID    uint64
	ProjectID string
	// Count is 1 to guestlink.MaxProcessesLeft, or 0 when no result came.
	Count uint32
	Rule  string
}

// gen is what the tracker holds for one VM generation of a slot.
type gen struct {
	generation uint64
	kills      map[uint64]*kill
	// left is the first result of processes left, nil while there is
	// none.
	left *Left
}

// Tracker holds the kills of every VM. Its methods are safe for
// concurrent use.
type Tracker struct {
	clock Clock
	log   *slog.Logger

	mu    sync.Mutex
	slots map[vm.Slot]*gen
}

// New returns a Tracker with no kill.
func New(cfg Config) *Tracker {
	t := &Tracker{clock: cfg.Clock, log: cfg.Logger, slots: map[vm.Slot]*gen{}}
	if t.clock == nil {
		t.clock = realClock{}
	}
	if t.log == nil {
		t.log = slog.Default()
	}
	return t
}

// genLocked returns what the tracker holds for slot's generation. A
// newer generation replaces an older one, whose refusals end with it,
// and whose pending kills close (S06-10-leftover-processes, "Failing
// closed"). It returns nil for a generation older than the slot's.
func (t *Tracker) genLocked(slot vm.Slot, generation uint64) *gen {
	g := t.slots[slot]
	switch {
	case g != nil && g.generation == generation:
		return g
	case g != nil && g.generation > generation:
		return nil
	case g != nil:
		t.closePendingLocked(slot, g, "a new generation started")
	}
	g = &gen{generation: generation, kills: map[uint64]*kill{}}
	t.slots[slot] = g
	return g
}

// Started tells the tracker that slot's VM generation runs. A newer
// generation ends the older one's refusals (S06-10-leftover-processes,
// "A result of processes left"), so ColdBootNext reads the new one.
func (t *Tracker) Started(slot vm.Slot, generation uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.genLocked(slot, generation)
}

// Begin records a kill of projectID's user in slot's VM generation and
// returns its kill ID, which the caller sends in KillUserRequest. The
// kill is pending until its result is accepted, its deadline passes, or
// its generation ends in a cold stop. A generation older than the
// slot's is refused.
func (t *Tracker) Begin(slot vm.Slot, generation uint64, projectID string) (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.genLocked(slot, generation)
	if g == nil {
		return 0, fmt.Errorf("kills: generation %d of the %s VM has ended", generation, slot)
	}
	// The guest keeps one record per user, and a newer one replaces an
	// older one (S06-10-leftover-processes, "Kill IDs and records"). So a
	// finished kill of the project is forgotten here, and its record, if
	// still listed, gets "end". A pending one stays until its deadline.
	for id, k := range g.kills {
		if k.projectID == projectID && k.status != pending {
			delete(g.kills, id)
		}
	}
	id := t.newIDLocked()
	g.kills[id] = &kill{id: id, projectID: projectID, deadline: t.clock.Now().Add(Deadline)}
	t.clock.AfterFunc(Deadline, func() { t.expire(slot) })
	t.log.LogAttrs(context.Background(), slog.LevelInfo, "kill sent",
		slog.String("slot", string(slot)), slog.Uint64("generation", generation), slog.Uint64("kill_id", id),
		slog.String("project", projectID), slog.String("rule", RuleSent), slog.String("decision", "send"),
		slog.Duration("deadline", Deadline))
	return id, nil
}

// newIDLocked returns a kill ID from crypto/rand that is never 0 and not
// in use by any VM (S06-10-leftover-processes, "Kill IDs and records").
func (t *Tracker) newIDLocked() uint64 {
	var b [8]byte
	for {
		// crypto/rand.Read never fails: it crashes the program instead.
		_, _ = rand.Read(b[:])
		id := binary.BigEndian.Uint64(b[:])
		if id != 0 && !t.inUseLocked(id) {
			return id
		}
	}
}

func (t *Tracker) inUseLocked(id uint64) bool {
	for _, g := range t.slots {
		if _, ok := g.kills[id]; ok {
			return true
		}
	}
	return false
}

// expire counts each pending kill of slot past its deadline as
// processes left. A deadline's timer calls it. Every check calls
// expireLocked first, so a check never waits for the timer.
func (t *Tracker) expire(slot vm.Slot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if g := t.slots[slot]; g != nil {
		t.expireLocked(slot, g)
	}
}

func (t *Tracker) expireLocked(slot vm.Slot, g *gen) {
	now := t.clock.Now()
	for _, k := range g.kills {
		if k.status != pending || now.Before(k.deadline) {
			continue
		}
		k.status = expired
		t.leftLocked(slot, g, Left{KillID: k.id, ProjectID: k.projectID, Rule: RuleDeadline})
	}
}

// ColdStop closes every pending kill of slot's generation, which ended
// in a cold stop: the guest it targeted no longer runs. Each one is
// logged with the rule and the stop's cause (S06-10-leftover-processes,
// "Failing closed"; SEC10-audit). A kill whose deadline passed first
// still counts as processes left.
func (t *Tracker) ColdStop(slot vm.Slot, generation uint64, cause string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.slots[slot]
	if g == nil || g.generation != generation {
		return
	}
	t.expireLocked(slot, g)
	t.closePendingLocked(slot, g, cause)
}

func (t *Tracker) closePendingLocked(slot vm.Slot, g *gen, cause string) {
	for _, k := range g.kills {
		if k.status != pending {
			continue
		}
		k.status = closed
		t.log.LogAttrs(context.Background(), slog.LevelInfo, "kill closed",
			slog.String("slot", string(slot)), slog.Uint64("generation", g.generation), slog.Uint64("kill_id", k.id),
			slog.String("project", k.projectID), slog.String("rule", RuleColdStop), slog.String("decision", "close"),
			slog.String("cause", cause))
	}
}
