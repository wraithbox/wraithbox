package kills

import (
	"context"
	"fmt"
	"log/slog"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// Where a result came from, for the log.
const (
	SourceReply  = "reply"
	SourceRecord = "record"
)

// verdict is what the tracker did with one result.
type verdict int

const (
	dropped verdict = iota
	taken
	takenBefore
)

// Accept takes a kill result for slot's VM generation when it passes
// every check of S06-10-leftover-processes, "Acceptance", and reports
// whether it did. A result it doesn't take is dropped and logged with
// its rule, and its kill stays pending until its deadline
// (SEC11-root-gains-nothing).
func (t *Tracker) Accept(slot vm.Slot, generation uint64, out guestlink.KillOutcome, source string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.acceptLocked(slot, generation, out, source) != dropped
}

// AcceptReply takes the result of the KillUser answer to the kill sentID
// of sentProject, as Accept does. An answer that names another kill or
// another project is dropped with RuleUnknownKill
// (proto/wraithbox/guest/v1, KillUserResponse).
func (t *Tracker) AcceptReply(slot vm.Slot, generation, sentID uint64, sentProject string, out guestlink.KillOutcome) bool {
	if out.KillID != sentID || out.ProjectID != sentProject {
		t.drop([]slog.Attr{
			slog.String("slot", string(slot)), slog.Uint64("generation", generation),
			slog.Uint64("kill_id", sentID), slog.Uint64("answer_kill_id", out.KillID),
			slog.String("project", sentProject), slog.String("source", SourceReply),
		}, RuleUnknownKill, "the answer is for another kill or project than the request")
		return false
	}
	return t.Accept(slot, generation, out, SourceReply)
}

// acceptLocked checks a result in the order of S06-10-leftover-processes,
// "Acceptance":
//
//   - the project ID passes the format check of S05-04-naming;
//   - the kill ID is one wb-hostd sent for that project, in this VM and
//     VM generation;
//   - it arrives before the kill's deadline, in that generation.
//
// A result for a kill whose result was taken before is taken again with
// no change, except that one of processes left still counts: a faked
// count only makes the host stricter.
func (t *Tracker) acceptLocked(slot vm.Slot, generation uint64, out guestlink.KillOutcome, source string) verdict {
	attrs := []slog.Attr{
		slog.String("slot", string(slot)), slog.Uint64("generation", generation),
		slog.Uint64("kill_id", out.KillID), slog.String("source", source),
	}
	if project.CheckID(out.ProjectID) != nil {
		// The ID is guest text of any form, so it isn't logged.
		t.drop(attrs, RuleProjectForm, "the project ID isn't 32 lowercase hexadecimal characters")
		return dropped
	}
	attrs = append(attrs, slog.String("project", out.ProjectID))
	// Only the slot's current generation takes a result. The lookup
	// creates no state, so a result can't start or end a generation.
	g := t.currentLocked(slot, generation)
	if g == nil {
		t.drop(attrs, RuleUnknownKill, "the VM generation isn't the one that runs in the slot")
		return dropped
	}
	k := g.kills[out.KillID]
	switch {
	case k == nil || k.projectID != out.ProjectID:
		t.drop(attrs, RuleUnknownKill, "wb-hostd sent no kill of this ID for this project in this VM generation")
		return dropped
	case k.status == expired || k.status == closed:
		t.drop(attrs, RuleLate, "the kill's deadline passed, or a cold stop closed it")
		return dropped
	case k.status == accepted:
		t.againLocked(slot, g, k, out, attrs)
		return takenBefore
	}
	k.status = accepted
	k.clean = out.Clean
	if out.Clean {
		t.log.LogAttrs(context.Background(), slog.LevelInfo, "kill result clean",
			append(attrs, slog.String("rule", RuleClean), slog.String("decision", "accept"))...)
		return taken
	}
	t.leftLocked(slot, g, leftOf(out))
	return taken
}

// againLocked acknowledges a result for a kill whose result was taken
// before, and logs the verdict it keeps (SEC10-audit). A clean result
// changes nothing. One of processes left still counts, so a clean verdict
// turns into processes left: a faked count only makes the host stricter.
func (t *Tracker) againLocked(slot vm.Slot, g *gen, k *kill, out guestlink.KillOutcome, attrs []slog.Attr) {
	if !out.Clean && k.clean {
		k.clean = false
		t.leftLocked(slot, g, leftOf(out))
	}
	kept := "processes-left"
	if k.clean {
		kept = "clean"
	}
	t.log.LogAttrs(context.Background(), slog.LevelInfo, "kill result again",
		append(attrs, slog.String("rule", RuleAgain), slog.String("decision", "acknowledge"), slog.String("kept", kept))...)
}

func leftOf(out guestlink.KillOutcome) Left {
	return Left{KillID: out.KillID, ProjectID: out.ProjectID, Count: out.ProcessesLeft, Rule: RuleProcessesLeft}
}

// leftLocked records a result of processes left for g, and logs it with
// the count, capped (S06-10-leftover-processes, "A result of processes
// left"). The first one is kept for the refusals.
func (t *Tracker) leftLocked(slot vm.Slot, g *gen, l Left) {
	if g.left == nil {
		g.left = &l
	}
	t.log.LogAttrs(context.Background(), slog.LevelWarn, "processes left in the VM",
		slog.String("slot", string(slot)), slog.Uint64("generation", g.generation),
		slog.Uint64("kill_id", l.KillID), slog.String("project", l.ProjectID),
		slog.String("processes_left", guestlink.CountLabel(l.Count)),
		slog.String("rule", l.Rule), slog.String("decision", "refuse-vm"))
}

func (t *Tracker) drop(attrs []slog.Attr, rule, reason string) {
	t.log.LogAttrs(context.Background(), slog.LevelWarn, "kill result dropped",
		append(attrs, slog.String("rule", rule), slog.String("decision", "drop"), slog.String("reason", reason))...)
}

// Answer is wb-hostd's answer to one cleanup record of the reconnect
// list (S06-11-reconnect-and-recovery, "Answers to cleanup records").
type Answer int

// The answers.
const (
	// AnswerEnd deletes the record.
	AnswerEnd Answer = iota
	// AnswerAcknowledged: the record's result was taken.
	AnswerAcknowledged
)

func (a Answer) String() string {
	if a == AnswerAcknowledged {
		return "acknowledged"
	}
	return "end"
}

// ListRefusal refuses a whole reconnect list. The caller closes the
// connection, which counts as a failed try.
type ListRefusal struct {
	Rule string
	msg  string
}

func (r *ListRefusal) Error() string { return "cleanup records refused (" + r.Rule + "): " + r.msg }

// AnswerRecords answers the cleanup records of a reconnect list from
// slot's VM generation, in their order. projectUsers is the count of
// project users of the VM. The whole list is refused when it holds more
// records than that, or two records with one kill ID
// (S06-11-reconnect-and-recovery, "The list"). A record gets
// "acknowledged" when its result passes Accept's checks or was taken
// before, and "end" otherwise.
func (t *Tracker) AnswerRecords(slot vm.Slot, generation uint64, records []*guestv1.CleanupRecord, projectUsers int) ([]Answer, error) {
	if r := checkList(records, projectUsers); r != nil {
		t.log.LogAttrs(context.Background(), slog.LevelWarn, "cleanup records refused",
			slog.String("slot", string(slot)), slog.Uint64("generation", generation), slog.Int("records", len(records)),
			slog.String("rule", r.Rule), slog.String("decision", "refuse"), slog.String("reason", r.msg))
		return nil, r
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	answers := make([]Answer, len(records))
	for i, rec := range records {
		out, r := guestlink.CheckCleanupRecord(rec)
		if r != nil {
			t.drop([]slog.Attr{
				slog.String("slot", string(slot)), slog.Uint64("generation", generation),
				slog.Uint64("kill_id", rec.GetKillId()), slog.String("source", SourceRecord),
			}, r.Rule, r.Error())
			continue
		}
		if t.acceptLocked(slot, generation, out, SourceRecord) != dropped {
			answers[i] = AnswerAcknowledged
		}
	}
	return answers, nil
}

// checkList refuses a list of more records than project users, or with
// two records of one kill ID. It counts before it looks at a record.
func checkList(records []*guestv1.CleanupRecord, projectUsers int) *ListRefusal {
	if len(records) > projectUsers {
		return &ListRefusal{Rule: RuleListTooMany, msg: fmt.Sprintf("%d records for %d project users", len(records), projectUsers)}
	}
	seen := make(map[uint64]bool, len(records))
	for _, rec := range records {
		if seen[rec.GetKillId()] {
			return &ListRefusal{Rule: RuleListDuplicate, msg: fmt.Sprintf("two records with the kill ID %d", rec.GetKillId())}
		}
		seen[rec.GetKillId()] = true
	}
	return nil
}
