package audit

import (
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
)

// The rules of the budgets (S09-11-audit, "Budgets"). wb-hostd tells a
// VM's senders through the event channel, and they refuse new work with
// the rule.
const (
	// RuleBudget: the VM's Network Activity and HTTP Activity records
	// reached audit_events_per_minute this calendar minute of UTC.
	RuleBudget = "audit-budget"
	// RuleBytes: the VM's records reached audit_vm_bytes_per_day this
	// calendar day of UTC.
	RuleBytes = "audit-bytes"
)

// Budget is a VM's use of its audit budgets, and the refusal they make,
// for the VM's status and the event channel's notices.
type Budget struct {
	// Rule is RuleBytes or RuleBudget while the VM is over a budget, and
	// "" otherwise. Bytes come first: their refusal lasts longer.
	Rule string
	// Until is when the refusal ends: the turn of the UTC day or minute.
	// Zero when Rule is "".
	Until time.Time
	// EventsThisMinute counts the VM's 4001 and 4002 records this
	// minute, and EventsPerMinute is the budget.
	EventsThisMinute, EventsPerMinute uint64
	// BytesToday counts the bytes of the VM's records today, and
	// BytesPerDay is the budget.
	BytesToday, BytesPerDay uint64
}

// usage is one VM's count in the current minute and day.
type usage struct {
	minute time.Time
	events uint64
	day    time.Time
	bytes  uint64
}

// countsAsEvent reports whether a record of class counts against
// audit_events_per_minute: the high-volume classes only (S09-11-audit,
// "Events per minute").
func countsAsEvent(class int) bool {
	return class == ClassNetworkActivity || class == ClassHTTPActivity
}

// countLocked counts a record of a VM's in its budgets, at the time the
// writer accepted it. Every record of the VM counts its bytes, the
// classes that are never dropped too (S09-11-audit, "Bytes per day").
// The record is written whatever the count: a budget refuses new work,
// and never drops a record. Callers hold w.mu.
func (w *Writer) countLocked(r Record, lineBytes uint64, at time.Time) {
	if r.Slot == "" {
		return
	}
	u := w.usage[r.Slot]
	if u == nil {
		u = &usage{}
		w.usage[r.Slot] = u
	}
	u.roll(at)
	u.bytes += lineBytes
	if countsAsEvent(r.ClassUID()) {
		u.events++
	}
}

// roll starts a new count when the minute or the day turned.
func (u *usage) roll(at time.Time) {
	minute, day := at.UTC().Truncate(time.Minute), utcDay(at)
	if !u.minute.Equal(minute) {
		u.minute, u.events = minute, 0
	}
	if !u.day.Equal(day) {
		u.day, u.bytes = day, 0
	}
}

func utcDay(at time.Time) time.Time {
	y, m, d := at.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Budget is the VM's use of its budgets now. A budget is over once the
// count reaches it, so the work that would pass it is refused.
func (w *Writer) Budget(slot string) Budget {
	now := w.cfg.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	b := Budget{EventsPerMinute: w.cfg.EventsPerMinute, BytesPerDay: w.cfg.VMBytesPerDay}
	u := w.usage[slot]
	if u == nil {
		return b
	}
	u.roll(now)
	b.EventsThisMinute, b.BytesToday = u.events, u.bytes
	switch {
	case b.BytesToday >= b.BytesPerDay:
		b.Rule, b.Until = RuleBytes, u.day.AddDate(0, 0, 1)
	case b.EventsThisMinute >= b.EventsPerMinute:
		b.Rule, b.Until = RuleBudget, u.minute.Add(time.Minute)
	}
	return b
}

// budgetDefaults sets the budgets cfg leaves zero to config's defaults.
func budgetDefaults(cfg *Config) {
	if cfg.EventsPerMinute == 0 {
		cfg.EventsPerMinute = config.DefaultAuditEventsPerMinute
	}
	if cfg.VMBytesPerDay == 0 {
		cfg.VMBytesPerDay = config.DefaultAuditVMMiBPerDay << 20
	}
}
