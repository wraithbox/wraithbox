package contracttest_test

import (
	"google.golang.org/protobuf/proto"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
)

// auditVM is a VM with every field set.
func auditVM() *auditv1.VM {
	return &auditv1.VM{Slot: auditv1.Slot_SLOT_ISOLATED, Generation: 7}
}

// auditEvent is an Event with every field set.
func auditEvent() *auditv1.Event {
	return &auditv1.Event{
		Class: auditv1.EventClass_EVENT_CLASS_HTTP_ACTIVITY, Decision: "refuse", Rule: "http-path-refused",
		DestinationHost: "api.github.com", DestinationPort: 443, HttpMethod: "POST", HttpPath: "/repos/o/r/issues",
		BytesIn: 1200, BytesOut: 340,
		GuestLabel: &auditv1.GuestLabel{Project: "myrepo", Session: "myrepo-20261101-1412-k3f9", User: "wbp1", Program: "node"},
	}
}

// auditRun is a Run with every field set.
func auditRun() *auditv1.Run {
	return &auditv1.Run{
		Class: auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY, Decision: "refuse", Rule: "audit-budget",
		Count: 42, WindowSeconds: 60, Open: true,
	}
}

// auditMessages holds a value of every message of the event channel
// (S04-08-records-and-bounds, "Event records"), each oneof member set in
// one of them.
func auditMessages() []proto.Message {
	return []proto.Message{
		auditVM(),
		auditEvent(),
		auditEvent().GetGuestLabel(),
		auditRun(),
		&auditv1.EventRecord{Sequence: 9, Vm: auditVM(), Kind: &auditv1.EventRecord_Event{Event: auditEvent()}},
		&auditv1.EventRecord{Sequence: 10, Vm: auditVM(), Kind: &auditv1.EventRecord_Run{Run: auditRun()}},
		&auditv1.BudgetNotice{Vm: auditVM(), Rule: auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, UntilUnix: 1_793_577_600},
	}
}
