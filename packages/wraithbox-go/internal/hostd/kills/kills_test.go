package kills

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// fakeClock moves only when a test advances it, and runs each AfterFunc
// that is due then, in order.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	funcs []fakeFunc
}

type fakeFunc struct {
	at time.Time
	f  func()
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.funcs = append(c.funcs, fakeFunc{at: c.now.Add(d), f: f})
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []func()
	kept := c.funcs[:0]
	for _, ff := range c.funcs {
		if ff.at.After(c.now) {
			kept = append(kept, ff)
			continue
		}
		due = append(due, ff.f)
	}
	c.funcs = kept
	c.mu.Unlock()
	for _, f := range due {
		f()
	}
}

// logs collects JSON log records.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// withRule returns the records of rule.
func (l *logs) withRule(t *testing.T, rule string) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for line := range bytes.Lines(l.buf.Bytes()) {
		rec := map[string]any{}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if rec["rule"] == rule {
			out = append(out, rec)
		}
	}
	return out
}

func newTracker(t *testing.T) (*Tracker, *fakeClock, *logs) {
	t.Helper()
	c, l := newFakeClock(), &logs{}
	tr := New(Config{Clock: c, Logger: slog.New(slog.NewJSONHandler(l, nil)), Audit: &audittest.Recorder{}})
	return tr, c, l
}

const (
	work     = vm.SlotWork
	isolated = vm.SlotIsolated
)

var (
	projA = strings.Repeat("a", 32)
	projB = strings.Repeat("b", 32)
)

func begin(t *testing.T, tr *Tracker, slot vm.Slot, generation uint64, projectID string) uint64 {
	t.Helper()
	id, err := tr.Begin(slot, generation, projectID)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return id
}

func clean(id uint64, projectID string) guestlink.KillOutcome {
	return guestlink.KillOutcome{KillID: id, ProjectID: projectID, Clean: true}
}

func left(id uint64, projectID string, n uint32) guestlink.KillOutcome {
	return guestlink.KillOutcome{KillID: id, ProjectID: projectID, ProcessesLeft: n}
}

func requireRule(t *testing.T, r *Refusal, rule string) {
	t.Helper()
	if r == nil || r.Rule != rule {
		t.Fatalf("refusal = %v, want the rule %s", r, rule)
	}
}

// wantFinding checks the one 2004 record with msg in the work VM's
// generation 1, its rule and its label of processes left, and returns
// its fields (S09-11-audit).
func wantFinding(t *testing.T, tr *Tracker, msg, rule, label string) audit.Fields {
	t.Helper()
	rec, ok := tr.audit.(*audittest.Recorder)
	if !ok {
		t.Fatal("the tracker's recorder isn't an audittest.Recorder")
	}
	recs := rec.Find(msg)
	if len(recs) != 1 {
		t.Fatalf("%d records %q, want 1", len(recs), msg)
	}
	r, f := recs[0], recs[0].Fields()
	gotLabel := ""
	if f.GuestLabel != nil {
		gotLabel = f.GuestLabel.ProcessesLeft
	}
	if r.ClassUID() != 2004 || r.Slot != string(work) || f.Generation != 1 || f.Rule != rule || f.Kill == nil || gotLabel != label {
		t.Fatalf("%q: class %d, slot %s, generation %d, rule %s, kill %+v, label %q; want 2004, work, 1, %s, a kill, %q",
			msg, r.ClassUID(), r.Slot, f.Generation, f.Rule, f.Kill, gotLabel, rule, label)
	}
	return f
}

func requireNone(t *testing.T, r *Refusal) {
	t.Helper()
	if r != nil {
		t.Fatalf("refused: %v", r)
	}
}

func requireLogged(t *testing.T, l *logs, rule, decision string) map[string]any {
	t.Helper()
	recs := l.withRule(t, rule)
	for _, rec := range recs {
		if rec["decision"] == decision {
			return rec
		}
	}
	t.Fatalf("no %s record with the decision %s in %v", rule, decision, recs)
	return nil
}

// TestKillCleanAccepted: a clean result of a kill that was sent is
// accepted and logged, and nothing is refused after it.
func TestKillCleanAccepted(t *testing.T) {
	tr, _, l := newTracker(t)
	id := begin(t, tr, work, 1, projA)
	requireLogged(t, l, RuleSent, "send")
	requireRule(t, tr.CheckSave(work, 1), RuleKillPending)
	requireRule(t, tr.CheckProject(work, 1, projA), RuleKillPending)
	requireNone(t, tr.CheckProject(work, 1, projB))
	if !tr.AcceptReply(work, 1, id, projA, clean(id, projA)) {
		t.Fatal("a clean answer to the kill was dropped")
	}
	requireLogged(t, l, RuleClean, "accept")
	requireNone(t, tr.CheckAdmission(work, 1))
	requireNone(t, tr.CheckSave(work, 1))
	requireNone(t, tr.CheckProject(work, 1, projA))
	if tr.ColdBootNext(work) {
		t.Fatal("a clean result asks for a cold boot")
	}
}

// TestKillAcceptanceRules: each rule of S06-10-leftover-processes,
// "Acceptance", drops a result and logs its rule, and the kill stays
// pending.
func TestKillAcceptanceRules(t *testing.T) {
	cases := []struct {
		name string
		// result returns the result and the slot and generation it
		// arrives in, for the kill id of projA sent in work, generation 2.
		result func(id uint64) (vm.Slot, uint64, guestlink.KillOutcome)
		rule   string
	}{
		{"project ID form", func(id uint64) (vm.Slot, uint64, guestlink.KillOutcome) {
			return work, 2, clean(id, strings.ToUpper(projA))
		}, RuleProjectForm},
		{"project ID control bytes", func(id uint64) (vm.Slot, uint64, guestlink.KillOutcome) {
			return work, 2, clean(id, "\x1b[2J"+projA[4:])
		}, RuleProjectForm},
		{"kill ID never sent", func(id uint64) (vm.Slot, uint64, guestlink.KillOutcome) {
			return work, 2, clean(id+1, projA)
		}, RuleUnknownKill},
		{"another project", func(id uint64) (vm.Slot, uint64, guestlink.KillOutcome) {
			return work, 2, clean(id, projB)
		}, RuleUnknownKill},
		{"another VM", func(id uint64) (vm.Slot, uint64, guestlink.KillOutcome) {
			return isolated, 2, clean(id, projA)
		}, RuleUnknownKill},
		{"an older generation", func(id uint64) (vm.Slot, uint64, guestlink.KillOutcome) {
			return work, 1, clean(id, projA)
		}, RuleUnknownKill},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, _, l := newTracker(t)
			tr.Started(work, 2)
			id := begin(t, tr, work, 2, projA)
			slot, generation, out := tc.result(id)
			if tr.Accept(slot, generation, out, SourceRecord) {
				t.Fatal("the result was accepted")
			}
			requireLogged(t, l, tc.rule, "drop")
			requireRule(t, tr.CheckSave(work, 2), RuleKillPending)
			requireNone(t, tr.CheckAdmission(work, 2))
		})
	}
}

// TestKillReplyForAnotherKill: a KillUser answer that names another
// kill, even one wb-hostd sent for the project, is dropped.
func TestKillReplyForAnotherKill(t *testing.T) {
	tr, _, l := newTracker(t)
	first := begin(t, tr, work, 1, projA)
	second := begin(t, tr, work, 1, projA)
	if tr.AcceptReply(work, 1, second, projA, clean(first, projA)) {
		t.Fatal("an answer for another kill was accepted")
	}
	if tr.AcceptReply(work, 1, second, projA, clean(second, projB)) {
		t.Fatal("an answer for another project was accepted")
	}
	if n := len(l.withRule(t, RuleUnknownKill)); n != 2 {
		t.Fatalf("%d %s records, want 2", n, RuleUnknownKill)
	}
}

// TestKillAnotherGeneration: a kill sent in one generation takes no
// result in the next, and the new generation isn't refused for it.
func TestKillAnotherGeneration(t *testing.T) {
	tr, c, l := newTracker(t)
	id := begin(t, tr, work, 1, projA)
	tr.ColdStop(work, 1, "stop requested")
	tr.Started(work, 2)
	if tr.Accept(work, 2, clean(id, projA), SourceRecord) {
		t.Fatal("a result of generation 1's kill was accepted in generation 2")
	}
	requireLogged(t, l, RuleUnknownKill, "drop")
	c.advance(Deadline)
	requireNone(t, tr.CheckAdmission(work, 2))
	requireNone(t, tr.CheckSave(work, 2))
}

// TestKillLateResult: a result after the deadline is dropped, and the
// kill counts as processes left.
func TestKillLateResult(t *testing.T) {
	tr, c, l := newTracker(t)
	id := begin(t, tr, work, 1, projA)
	c.advance(Deadline)
	if tr.AcceptReply(work, 1, id, projA, clean(id, projA)) {
		t.Fatal("a late result was accepted")
	}
	requireLogged(t, l, RuleLate, "drop")
	requireRule(t, tr.CheckAdmission(work, 1), RuleVMProcessesLeft)
}

// TestKillMissingResult: a kill without a result counts as processes
// left at its deadline, and not a moment before.
func TestKillMissingResult(t *testing.T) {
	tr, c, l := newTracker(t)
	begin(t, tr, work, 1, projA)
	c.advance(Deadline - time.Second)
	requireNone(t, tr.CheckAdmission(work, 1))
	requireRule(t, tr.CheckSave(work, 1), RuleKillPending)
	c.advance(time.Second)
	rec := requireLogged(t, l, RuleDeadline, "refuse-vm")
	if rec["processes_left"] != "unknown" {
		t.Fatalf("processes_left = %v, want unknown", rec["processes_left"])
	}
	wantFinding(t, tr, "processes left in the VM", RuleDeadline, "unknown")
	requireRule(t, tr.CheckAdmission(work, 1), RuleVMProcessesLeft)
	requireRule(t, tr.CheckSave(work, 1), RuleVMProcessesLeft)
	if !tr.ColdBootNext(work) {
		t.Fatal("processes left don't ask for a cold boot")
	}
}

// TestKillProcessesLeft: a result of processes left refuses new sessions,
// learn mode and saves in the generation, logs the count capped, and asks
// for a cold boot. Only a new generation clears it.
func TestKillProcessesLeft(t *testing.T) {
	tr, c, l := newTracker(t)
	tr.Started(work, 1)
	id := begin(t, tr, work, 1, projA)
	if !tr.AcceptReply(work, 1, id, projA, left(id, projA, guestlink.MaxProcessesLeft)) {
		t.Fatal("a result of processes left was dropped")
	}
	rec := requireLogged(t, l, RuleProcessesLeft, "refuse-vm")
	if rec["processes_left"] != "more than 1000" {
		t.Fatalf("processes_left = %v, want more than 1000", rec["processes_left"])
	}
	wantFinding(t, tr, "processes left in the VM", RuleProcessesLeft, "more than 1000")
	r := tr.CheckAdmission(work, 1)
	requireRule(t, r, RuleVMProcessesLeft)
	if !strings.Contains(r.Message, "more than 1000") || !strings.Contains(r.Message, "wb vm stop work") {
		t.Fatalf("message %q", r.Message)
	}
	requireLogged(t, l, RuleVMProcessesLeft, "refuse")
	requireRule(t, tr.CheckSave(work, 1), RuleVMProcessesLeft)
	// Another VM isn't refused.
	requireNone(t, tr.CheckAdmission(isolated, 1))

	// A later clean result doesn't clear it, and the deadline doesn't
	// either.
	if !tr.AcceptReply(work, 1, id, projA, clean(id, projA)) {
		t.Fatal("a result taken before wasn't acknowledged")
	}
	// The clean result is logged only as the same result again, with the
	// verdict kept.
	if recs := l.withRule(t, RuleClean); len(recs) != 0 {
		t.Fatalf("a clean result logged as accepted after processes left: %v", recs)
	}
	if rec := requireLogged(t, l, RuleAgain, "acknowledge"); rec["kept"] != "processes-left" {
		t.Fatalf("record %v, want the kept verdict processes-left", rec)
	}
	c.advance(Deadline)
	requireRule(t, tr.CheckAdmission(work, 1), RuleVMProcessesLeft)

	tr.ColdStop(work, 1, "stop requested")
	requireRule(t, tr.CheckAdmission(work, 1), RuleVMProcessesLeft)
	if !tr.ColdBootNext(work) {
		t.Fatal("the next start isn't a cold boot")
	}
	tr.Started(work, 2)
	requireNone(t, tr.CheckAdmission(work, 2))
	requireNone(t, tr.CheckSave(work, 2))
	if tr.ColdBootNext(work) {
		t.Fatal("generation 2 still asks for a cold boot")
	}
}

// TestKillColdStop: a cold stop closes a pending kill, logged with its
// cause, and the kill never counts as processes left after it.
func TestKillColdStop(t *testing.T) {
	tr, c, l := newTracker(t)
	id := begin(t, tr, work, 1, projA)
	begin(t, tr, isolated, 1, projA)
	tr.ColdStop(work, 1, "stopped unexpectedly")
	rec := requireLogged(t, l, RuleColdStop, "close")
	if rec["cause"] != "stopped unexpectedly" || rec["kill_id"] != float64(id) {
		t.Fatalf("record %v", rec)
	}
	if f := wantFinding(t, tr, "kill closed", RuleColdStop, ""); f.Cause != "stopped unexpectedly" || f.Kill.ID != id {
		t.Errorf("kill closed record: cause %q, kill %+v", f.Cause, f.Kill)
	}
	requireNone(t, tr.CheckSave(work, 1))
	c.advance(Deadline)
	requireNone(t, tr.CheckAdmission(work, 1))
	if tr.ColdBootNext(work) {
		t.Fatal("a closed kill asks for a cold boot")
	}
	if tr.Accept(work, 1, clean(id, projA), SourceRecord) {
		t.Fatal("a result of a closed kill was accepted")
	}
	requireLogged(t, l, RuleLate, "drop")
	// The other VM's kill wasn't closed, and failed closed.
	requireRule(t, tr.CheckAdmission(isolated, 1), RuleVMProcessesLeft)
}

// TestKillColdStopAfterDeadline: a kill whose deadline passed before the
// cold stop still counts as processes left.
func TestKillColdStopAfterDeadline(t *testing.T) {
	tr, c, _ := newTracker(t)
	begin(t, tr, work, 1, projA)
	c.mu.Lock()
	c.now = c.now.Add(Deadline) // the timer hasn't run yet
	c.mu.Unlock()
	tr.ColdStop(work, 1, "stop requested")
	if !tr.ColdBootNext(work) {
		t.Fatal("a kill past its deadline was closed")
	}
}

// TestKillBeginOldGeneration: no kill is sent to a generation that has
// ended, by a newer one or by a cold stop.
func TestKillBeginOldGeneration(t *testing.T) {
	tr, _, _ := newTracker(t)
	tr.Started(work, 3)
	if _, err := tr.Begin(work, 2, projA); err == nil {
		t.Fatal("a kill began in an older generation")
	}
	tr.ColdStop(work, 3, "stop requested")
	if _, err := tr.Begin(work, 3, projA); err == nil {
		t.Fatal("a kill began in a generation a cold stop ended")
	}
	requireNone(t, tr.CheckSave(work, 3))
}

// TestKillResultNewerGeneration: a result for a generation newer than the
// slot's is dropped, and doesn't start that generation: the slot's kills
// stay pending.
func TestKillResultNewerGeneration(t *testing.T) {
	tr, _, l := newTracker(t)
	id := begin(t, tr, work, 1, projA)
	if tr.Accept(work, 2, clean(id, projA), SourceRecord) {
		t.Fatal("a result for generation 2 was accepted")
	}
	requireLogged(t, l, RuleUnknownKill, "drop")
	requireRule(t, tr.CheckProject(work, 1, projA), RuleKillPending)
	if !tr.Accept(work, 1, clean(id, projA), SourceRecord) {
		t.Fatal("generation 1's result was dropped after a result for generation 2")
	}
}

// TestKillResultAgain: a result again for a clean kill is acknowledged
// and logged with the verdict kept. One of processes left still counts.
func TestKillResultAgain(t *testing.T) {
	tr, _, l := newTracker(t)
	id := begin(t, tr, work, 1, projA)
	for range 2 {
		if !tr.Accept(work, 1, clean(id, projA), SourceReply) {
			t.Fatal("clean result dropped")
		}
	}
	if rec := requireLogged(t, l, RuleAgain, "acknowledge"); rec["kept"] != "clean" {
		t.Fatalf("record %v, want the kept verdict clean", rec)
	}
	requireNone(t, tr.CheckAdmission(work, 1))
	if !tr.Accept(work, 1, left(id, projA, 2), SourceRecord) {
		t.Fatal("a result of processes left again was dropped")
	}
	requireRule(t, tr.CheckAdmission(work, 1), RuleVMProcessesLeft)
}

// TestKillIDs: kill IDs are never 0, and never repeat among the kills
// held.
func TestKillIDs(t *testing.T) {
	tr, _, _ := newTracker(t)
	seen := map[uint64]bool{}
	for range 1000 {
		id := begin(t, tr, work, 1, projA)
		if id == 0 || seen[id] {
			t.Fatalf("kill ID %d (seen %v)", id, seen[id])
		}
		seen[id] = true
	}
}

func record(id uint64, projectID string, result guestv1.KillResult, n uint32) *guestv1.CleanupRecord {
	return &guestv1.CleanupRecord{KillId: id, ProjectId: projectID, Result: result, ProcessesLeft: n}
}

// TestListRefused: a reconnect list with more cleanup records than
// project users, or two with one kill ID, is refused whole.
func TestListRefused(t *testing.T) {
	cleanR := guestv1.KillResult_KILL_RESULT_CLEAN
	cases := []struct {
		name    string
		records []*guestv1.CleanupRecord
		users   int
		rule    string
	}{
		{"too many", []*guestv1.CleanupRecord{record(1, projA, cleanR, 0), record(2, projB, cleanR, 0)}, 1, RuleListTooMany},
		{"none allowed", []*guestv1.CleanupRecord{record(1, projA, cleanR, 0)}, 0, RuleListTooMany},
		{"one kill ID twice", []*guestv1.CleanupRecord{record(7, projA, cleanR, 0), record(7, projB, cleanR, 0)}, 2, RuleListDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, _, l := newTracker(t)
			answers, err := tr.AnswerRecords(work, 1, tc.records, tc.users)
			lr, ok := errors.AsType[*ListRefusal](err)
			if !ok || lr.Rule != tc.rule || answers != nil {
				t.Fatalf("answers %v, err %v, want the rule %s", answers, err, tc.rule)
			}
			requireLogged(t, l, tc.rule, "refuse")
		})
	}
}

// TestListAnswers: a record that passes the checks, or was taken before,
// gets "acknowledged", and every other one "end". A record of processes
// left counts as a reply of it.
func TestListAnswers(t *testing.T) {
	tr, _, _ := newTracker(t)
	projC := strings.Repeat("c", 32)
	projD := strings.Repeat("d", 32)
	fresh := begin(t, tr, work, 1, projA)
	before := begin(t, tr, work, 1, projB)
	if !tr.AcceptReply(work, 1, before, projB, clean(before, projB)) {
		t.Fatal("reply dropped")
	}
	unknown := begin(t, tr, isolated, 1, projC)
	malformed := begin(t, tr, work, 1, projD)
	cleanR := guestv1.KillResult_KILL_RESULT_CLEAN
	records := []*guestv1.CleanupRecord{
		record(fresh, projA, guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT, 2),
		record(before, projB, cleanR, 0),
		record(unknown, projC, cleanR, 0),
		record(malformed, projD, guestv1.KillResult_KILL_RESULT_UNSPECIFIED, 0),
		record(malformed+1, "not-a-project", cleanR, 0),
	}
	answers, err := tr.AnswerRecords(work, 1, records, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []Answer{AnswerAcknowledged, AnswerAcknowledged, AnswerEnd, AnswerEnd, AnswerEnd}
	for i := range want {
		if answers[i] != want[i] {
			t.Fatalf("answer %d = %s, want %s (all %v)", i, answers[i], want[i], answers)
		}
	}
	// The record of processes left refuses the VM, and the malformed one
	// left its kill pending.
	requireRule(t, tr.CheckAdmission(work, 1), RuleVMProcessesLeft)
	requireRule(t, tr.CheckProject(work, 1, projD), RuleKillPending)
}
