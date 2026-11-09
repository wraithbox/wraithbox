package handoff

import (
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// The control messages that end what a VM's previous run or active
// period left: a fresh HandoffSocket and ActivePeriodStart
// (S07-06-name-mapping, "Lifetime", "Wildcard budget, second layer").

// TestFreshStartClears gives VM A a fresh hand-off socket after its copy
// holds two entries, one counted under the wildcard budget, and its
// policy allows them. The old socket and its stream close, the copy and
// the count are empty, and with no policy a report is refused until the
// run's first PolicyChange, whose sequence starts at 1 again. VM B keeps
// its copy.
func TestFreshStartClears(t *testing.T) {
	h, a, b := twoVMs(t, DefaultCaps())
	a.report("a.example.com", wire(1))
	a.report("x.wild.example.com", wire(2))
	open := a.stream(genA, wire(1), 443, 1)
	h.expectStream()
	b.report("b.example.org", wire(1))
	b.barrier()
	beforeB := h.copyOf("isolated")

	a2 := h.addFreshVM("work", genA+1)
	expectEOF(t, open)
	a.expectEnded()
	h.expectLog(`"rule":"vm-fresh-start"`, `"vm":"work"`, `"decision":"clear"`)
	if c := h.copyOf("work"); len(c.byAddr) != 0 || len(c.wildcard) != 0 {
		t.Fatalf("copy after a fresh start = %+v, want empty", c)
	}
	if c := h.copyOf("isolated"); !c.equal(beforeB) {
		t.Fatalf("VM B's copy changed: %+v, was %+v", c, beforeB)
	}

	// No policy until the run's first PolicyChange (SEC05-default-deny).
	a2.report("a.example.com", wire(1))
	h.expectEvent("work", RuleNameReportRefused, CheckNotAllowed)
	a2.stream(genA+1, wire(1), 443, 2)
	a2.expectReset(2)
	h.expectEvent("work", RuleStreamHandoffRefused, CheckNoEntry)

	h.policy("work", 1, policyProto(1, endpoint("*.wild.example.com", 443)))
	// wire(1) held a.example.com in the previous run: no conflict now,
	// and the budget of 1 holds no name of that run.
	a2.report("y.wild.example.com", wire(1))
	a2.stream(genA+1, wire(1), 443, 3)
	if s := h.expectStream(); s.Name.String() != "y.wild.example.com" {
		t.Fatalf("stream after the fresh start = %+v", s)
	}
	if got := h.wild.written(); len(got) != 2 || got[1] != "work/y.wild.example.com" {
		t.Fatalf("wildcard names written = %q", got)
	}
}

// TestReplacementKeepsState gives VM A a newer hand-off socket that
// isn't fresh, as at a restore or a daemon restart. Its copy, its count
// and its policy carry over.
func TestReplacementKeepsState(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.report("x.wild.example.com", wire(2))
	a.barrier()
	before := h.copyOf("work")
	a2 := h.addVM("work", genA+1)
	a.expectEnded()
	if c := h.copyOf("work"); !c.equal(before) {
		t.Fatalf("copy after a replacement = %+v, was %+v", c, before)
	}
	a2.stream(genA+1, wire(2), 443, 1)
	h.expectStream()
	a2.report("y.wild.example.com", wire(3))
	h.expectEvent("work", RuleNameReportRefused, CheckWildcardBudget)
}

// TestFreshOlderGenerationRefused: a fresh HandoffSocket is still
// refused for a generation older than the one held, and nothing is
// cleared before the control channel ends.
func TestFreshOlderGenerationRefused(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.report("a.example.com", wire(1))
	a.barrier()
	x, _ := socketpair(t, syscall.SOCK_DGRAM)
	m := handoffSocket("work", genA-1)
	m.GetHandoffSocket().Fresh = true
	h.send(m, x)
	h.expectEvent("work", RuleHandoffSocketRefused, CheckGenerationOlder)
	h.expectServeEnded()
	a.expectEnded()
	if c := h.copyOf("work"); len(c.byAddr) != 1 {
		t.Fatalf("a refused fresh socket cleared the copy: %+v", c)
	}
}

func activePeriodStart(vm string) *egressv1.HostdToProxyd {
	return &egressv1.HostdToProxyd{Kind: &egressv1.HostdToProxyd_ActivePeriodStart{
		ActivePeriodStart: &egressv1.ActivePeriodStart{Vm: vm},
	}}
}

// TestActivePeriodStart fills VM A's wildcard budget of 1, starts a new
// active period, and sees a new name pass while the copy keeps its
// entries.
func TestActivePeriodStart(t *testing.T) {
	h, a, _ := twoVMs(t, DefaultCaps())
	a.report("x.wild.example.com", wire(2))
	a.report("y.wild.example.com", wire(3))
	h.expectEvent("work", RuleNameReportRefused, CheckWildcardBudget)

	h.send(activePeriodStart("work"), nil)
	// The control channel is decided in order, so the PolicyAck says the
	// ActivePeriodStart applied.
	h.policy("work", 2, policyProto(1, endpoint("*.wild.example.com", 443)))
	h.expectLog(`"rule":"active-period-start"`, `"vm":"work"`)

	a.report("y.wild.example.com", wire(3))
	a.stream(genA, wire(3), 443, 1)
	if s := h.expectStream(); s.Name.String() != "y.wild.example.com" {
		t.Fatalf("stream after the new period = %+v", s)
	}
	a.stream(genA, wire(2), 443, 2)
	if s := h.expectStream(); s.Name.String() != "x.wild.example.com" {
		t.Fatalf("stream to an entry of the old period = %+v", s)
	}
	a.report("z.wild.example.com", wire(4))
	h.expectEvent("work", RuleNameReportRefused, CheckWildcardBudget)
}

// TestActivePeriodStartRefused: an ActivePeriodStart with a descriptor,
// or for a VM that isn't a VM slot, is refused with
// control-message-refused and ends the control channel, and with it
// every hand-off socket.
func TestActivePeriodStartRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		send  func(h *harness)
		check egressmsg.Check
	}{
		{"a descriptor", func(h *harness) {
			x, _ := socketpair(h.t, syscall.SOCK_DGRAM)
			h.send(activePeriodStart("work"), x)
		}, egressmsg.CheckMessageForm},
		{"the build VM", func(h *harness) { h.send(activePeriodStart("build"), nil) }, egressmsg.CheckVMSlot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, a, _ := twoVMs(t, DefaultCaps())
			tc.send(h)
			h.expectEvent("", RuleControlRefused, tc.check)
			h.expectServeEnded()
			a.expectEnded()
		})
	}
}
