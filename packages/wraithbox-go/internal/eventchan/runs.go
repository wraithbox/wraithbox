package eventchan

import (
	"time"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
)

// runKey names one run: the refusals of one VM, class and rule
// (S09-11-audit, "Runs with a count").
type runKey struct {
	vm       VM
	class    auditv1.EventClass
	decision string
	rule     string
}

// run is the count of a run in its current window.
type run struct {
	// window is the start of the calendar UTC minute the count is for.
	window time.Time
	count  uint64
}

// CountRun counts one event of a class the daemon aggregates
// (S07-02-packet-path, "Events and rate limits"), in place of its own
// record. The run's count is written at the end of its minute, or as an
// open run at EndVM. A decision or rule that isn't of the form a record
// takes is counted under event-record-unsendable, so the count still
// reaches the log.
func (s *Sender) CountRun(vm VM, class auditv1.EventClass, decision, rule string) {
	if !ValidDecision(decision) || !ValidRule(rule) {
		s.log.Error("run not of a record's form", "rule", string(RuleUnsendable), "decision", "count",
			"run_decision", Cut(decision, MaxDecision), "run_rule", Cut(rule, MaxRule))
		decision, rule = "refuse", string(RuleUnsendable)
	}
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.countLocked(runKey{vm: vm, class: class, decision: decision, rule: rule}, now)
}

// countLocked counts one event in its run, and writes the run's count
// first when its window has ended or its count is at MaxRunCount.
func (s *Sender) countLocked(k runKey, now time.Time) {
	window := now.UTC().Truncate(time.Minute)
	r, ok := s.runs[k]
	if ok && (!r.window.Equal(window) || r.count >= MaxRunCount) {
		s.flushRunLocked(k, r, now, false)
		ok = false
	}
	if !ok {
		r = &run{window: window}
		s.runs[k] = r
	}
	r.count++
}

// endWindows writes the count of each run whose minute has ended.
func (s *Sender) endWindows(now time.Time) {
	window := now.UTC().Truncate(time.Minute)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, r := range s.runs {
		if r.window.Before(window) {
			s.flushRunLocked(k, r, now, false)
		}
	}
}

// flushRunLocked queues the record of a run and forgets the run. An
// open run is one whose window hasn't ended, written at EndVM for the
// VM's stop record.
func (s *Sender) flushRunLocked(k runKey, r *run, now time.Time, open bool) {
	delete(s.runs, k)
	secs := uint32(runWindowSeconds)
	if open {
		secs = uint32(min(max(now.Sub(r.window)/time.Second, 1), runWindowSeconds))
	}
	s.enqueueLocked(&auditv1.EventRecord{Vm: k.vm.proto(), Kind: &auditv1.EventRecord_Run{Run: &auditv1.Run{
		Class: k.class, Decision: k.decision, Rule: k.rule,
		Count: r.count, WindowSeconds: secs, Open: open,
	}}})
}
