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
// open run at EndVM. A class, decision or rule that isn't of the form a
// record takes is counted under event-record-not-encoded, so the count
// still reaches the log.
func (s *Sender) CountRun(vm VM, class auditv1.EventClass, decision, rule string) {
	k := s.formOf(runKey{vm: vm, class: class, decision: decision, rule: rule}, 0)
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.countLocked(k, now)
}

// formOf returns k as is when a record may carry its class, decision,
// rule and port. Otherwise it logs the bad form, and returns the run of
// k's VM that counts such events: a refusal under
// event-record-not-encoded, of a Detection Finding when the class isn't
// known. wb-hostd would end a sender that wrote the bad form.
func (s *Sender) formOf(k runKey, port uint32) runKey {
	if KnownClass(k.class) && ValidDecision(k.decision) && ValidRule(k.rule) && port <= MaxPort {
		return k
	}
	s.log.Error("event not of a record's form, counted", "rule", string(RuleNotEncoded), "decision", "count",
		"class", int32(k.class), "event_decision", Cut(k.decision, MaxDecision), "event_rule", Cut(k.rule, MaxRule),
		"port", port)
	if !KnownClass(k.class) {
		k.class = auditv1.EventClass_EVENT_CLASS_DETECTION_FINDING
	}
	k.decision, k.rule = "refuse", string(RuleNotEncoded)
	return k
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
