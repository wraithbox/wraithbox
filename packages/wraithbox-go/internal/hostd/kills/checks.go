package kills

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// Refusal is a request the kills of a VM generation refuse, with the rule
// and what to do (NFR06-explained-refusals).
type Refusal struct {
	Rule    string
	Message string
}

func (r *Refusal) Error() string { return r.Message + " (" + r.Rule + ")" }

// currentLocked returns slot's state when it is of generation, after it has
// counted every kill past its deadline. nil when it holds another
// generation, or none.
func (t *Tracker) currentLocked(slot vm.Slot, generation uint64) *gen {
	g := t.slots[slot]
	if g == nil || g.generation != generation {
		return nil
	}
	t.expireLocked(slot, g)
	return g
}

// CheckAdmission refuses every new session in slot's VM generation, of
// any project, and learn mode, after a result of processes left in it
// (S06-10-leftover-processes, "A result of processes left";
// SEC08-proj-isolation). Sessions that already run continue. Only a cold
// boot, which starts a new generation, clears it.
func (t *Tracker) CheckAdmission(slot vm.Slot, generation uint64) *Refusal {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.currentLocked(slot, generation)
	if g == nil || g.left == nil {
		return nil
	}
	return t.refuse(slot, generation, "session refused", leftRefusal(slot, g.left))
}

// CheckProject refuses a new session of projectID in slot's VM
// generation while a kill of its user is pending: the kill would stop
// the session's processes.
func (t *Tracker) CheckProject(slot vm.Slot, generation uint64, projectID string) *Refusal {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.currentLocked(slot, generation)
	if g == nil {
		return nil
	}
	for _, k := range g.kills {
		if k.status == pending && k.projectID == projectID {
			return t.refuse(slot, generation, "session refused", &Refusal{Rule: RuleKillPending, Message: fmt.Sprintf(
				"wb-hostd is waiting for wb-guestd in the %s VM to stop the project's processes. Try again in a minute.", slot)})
		}
	}
	return nil
}

// CheckSave refuses a save of slot's VM generation, by idle suspend or
// `wb vm suspend`, while a kill in it is pending, and after a result of
// processes left (S06-09-session-lifecycle, "Idle suspend").
func (t *Tracker) CheckSave(slot vm.Slot, generation uint64) *Refusal {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.currentLocked(slot, generation)
	switch {
	case g == nil:
		return nil
	case g.left != nil:
		return t.refuse(slot, generation, "save refused", leftRefusal(slot, g.left))
	}
	for _, k := range g.kills {
		if k.status == pending {
			return t.refuse(slot, generation, "save refused", &Refusal{Rule: RuleKillPending, Message: fmt.Sprintf(
				"the %s VM isn't saved while wb-guestd stops a project's processes. Try again in a few minutes.", slot)})
		}
	}
	return nil
}

// ColdBootNext reports whether slot's next start must be a cold boot:
// its last VM generation had a result of processes left. A start with a
// saved state then deletes it (S06-10-leftover-processes, "A result of
// processes left").
func (t *Tracker) ColdBootNext(slot vm.Slot) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.slots[slot]
	if g == nil {
		return false
	}
	t.expireLocked(slot, g)
	return g.left != nil
}

func leftRefusal(slot vm.Slot, l *Left) *Refusal {
	return &Refusal{Rule: RuleVMProcessesLeft, Message: fmt.Sprintf(
		"processes of a project user were left in the %s VM (count: %s), so no new session or learn mode starts in it, "+
			"and it isn't saved, until it boots again. Stop it with wb vm stop %s once no session runs in it, "+
			"then start it with wb vm start %s.", slot, guestlink.CountLabel(l.Count), slot, slot)}
}

func (t *Tracker) refuse(slot vm.Slot, generation uint64, what string, r *Refusal) *Refusal {
	t.log.LogAttrs(context.Background(), slog.LevelWarn, what,
		slog.String("slot", string(slot)), slog.Uint64("generation", generation),
		slog.String("rule", r.Rule), slog.String("decision", "refuse"))
	return r
}
