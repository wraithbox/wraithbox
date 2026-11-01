package audit

import (
	"strings"
	"testing"
	"time"
)

func netRecord() Record {
	return EventFromChannel(From{Slot: "work", Generation: 1, Sender: "wb-netd", Sequence: 1},
		ChannelEvent{ClassUID: ClassNetworkActivity, Decision: "allow", Rule: "egress-allow", Host: "example.com", Port: 443})
}

// TestEventsBudget: a VM's 4001 and 4002 records reach
// audit_events_per_minute, the budget refuses with audit-budget until
// the UTC minute turns, every record is still written, and the other VM
// is not refused (S09-11-audit, "Events per minute").
func TestEventsBudget(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.EventsPerMinute = 5 })
	for range 4 {
		h.w.Record(netRecord())
	}
	h.w.Record(VMRunning("work", 1, "vm-start")) // a 6002 record counts bytes only
	if b := h.w.Budget("work"); b.Rule != "" || b.EventsThisMinute != 4 || b.EventsPerMinute != 5 {
		t.Fatalf("under the budget: %+v", b)
	}
	h.w.Record(netRecord())
	h.w.Record(netRecord())
	b := h.w.Budget("work")
	if b.Rule != RuleBudget || !b.Until.Equal(start.Add(time.Minute)) || b.EventsThisMinute != 6 {
		t.Fatalf("over the budget: %+v", b)
	}
	if other := h.w.Budget("isolated"); other.Rule != "" {
		t.Fatalf("the other VM: %+v", other)
	}
	h.flush()
	if n := len(h.records(start)); n != 7 {
		t.Fatalf("%d records written, want all 7", n)
	}
	h.advance(time.Minute)
	if b := h.w.Budget("work"); b.Rule != "" || b.EventsThisMinute != 0 {
		t.Fatalf("after the minute: %+v", b)
	}
}

// TestBytesBudget: every record of a VM counts its bytes against
// audit_vm_bytes_per_day, the never-dropped classes too. Past it the
// rule is audit-bytes until the UTC day turns, which outranks
// audit-budget (S09-11-audit, "Bytes per day").
func TestBytesBudget(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.VMBytesPerDay = 4 << 10; c.EventsPerMinute = 1 })
	h.w.Record(VMRunning("work", 1, "vm-start"))
	b := h.w.Budget("work")
	if b.Rule != "" || b.BytesToday == 0 || b.BytesPerDay != 4<<10 {
		t.Fatalf("under the budget: %+v", b)
	}
	per := b.BytesToday
	for range int(4<<10/per) + 1 {
		h.w.Record(VMRunning("work", 1, "vm-start"))
	}
	h.w.Record(netRecord())
	b = h.w.Budget("work")
	if b.Rule != RuleBytes || !b.Until.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("over the budget: %+v", b)
	}
	h.w.Record(ShellOpened("work", 1, "shell-open", Session{ID: "s"}, nil))
	h.flush()
	if msgs := h.messages(start); msgs[len(msgs)-1] != "shell open" {
		t.Fatalf("the never-dropped record wasn't written: %q", msgs[len(msgs)-1])
	}
	h.advance(12 * time.Hour)
	if b := h.w.Budget("work"); b.Rule != "" || b.BytesToday != 0 {
		t.Fatalf("after the day: %+v", b)
	}
}

// TestBudgetOfNoVM: a record of no VM counts against no VM's budget.
func TestBudgetOfNoVM(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.VMBytesPerDay = 1 })
	h.w.Record(GitChecked("accept", "host-git-version", Git{}))
	for _, slot := range []string{"", "work"} {
		if b := h.w.Budget(slot); b.Rule != "" || b.BytesToday != 0 {
			t.Fatalf("budget of %q: %+v", slot, b)
		}
	}
}

func TestBudgetDefaults(t *testing.T) {
	h := newHarness(t, nil)
	b := h.w.Budget("work")
	if b.EventsPerMinute != 6000 || b.BytesPerDay != 64<<20 {
		t.Fatalf("defaults: %+v", b)
	}
}

// TestChannelGuestText: the guest's strings of a record of the event
// channel lose the escaped code points and are cut again before the
// write.
func TestChannelGuestText(t *testing.T) {
	r := EventFromChannel(channelFrom, ChannelEvent{
		ClassUID: ClassHTTPActivity, Decision: "allow", Rule: "r",
		Host: "a\x1b[31m" + strings.Repeat("h", 400), Method: "GET\u202e", Path: strings.Repeat("p", 2000),
		Label: GuestLabel{Program: "\x1b]0;x\x07" + strings.Repeat("é", 300)},
	})
	e, ok := r.Event.(*HTTPActivity)
	if !ok {
		t.Fatalf("event %T", r.Event)
	}
	host, path, program := e.DstEndpoint.Hostname, e.HTTPRequest.URL.Path, r.Fields().GuestLabel.Program
	if strings.ContainsAny(host+e.HTTPRequest.HTTPMethod+program, "\x1b\u202e\x07") {
		t.Errorf("escaped code points kept: %q %q %q", host, e.HTTPRequest.HTTPMethod, program)
	}
	if len(host) > maxHost || len(path) > maxPath || len(program) > maxGuestLabel {
		t.Errorf("not cut: host %d path %d program %d", len(host), len(path), len(program))
	}
	if e.HTTPRequest.HTTPMethod != "GET" || e.ActivityID != 3 {
		t.Errorf("method %q activity %d, want GET 3", e.HTTPRequest.HTTPMethod, e.ActivityID)
	}
}
