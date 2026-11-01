package vm_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

// checkRecord checks the one record with msg: the work VM, generation 1,
// rule and the OCSF status.
func checkRecord(t *testing.T, h *harness, msg, rule string, status int) {
	t.Helper()
	recs := h.audit.Find(msg)
	if len(recs) != 1 {
		t.Fatalf("%d records %q, want 1 (records %q)", len(recs), msg, h.audit.Messages())
	}
	f := recs[0].Fields()
	if recs[0].Slot != string(vm.SlotWork) || f.Generation != 1 || f.Rule != rule || recs[0].StatusID() != status {
		t.Errorf("%q: slot %s, generation %d, rule %s, status %d; want work, 1, %s, %d",
			msg, recs[0].Slot, f.Generation, f.Rule, recs[0].StatusID(), rule, status)
	}
}

const (
	statusSuccess = 1
	statusFailure = 2
)

// waitStopRecord waits for the stop record, which a run's end adds on
// another goroutine.
func waitStopRecord(t *testing.T, h *harness) {
	const msg = "vm stopped"
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.audit.Find(msg)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no record %q (records %q)", msg, h.audit.Messages())
		}
		time.Sleep(time.Millisecond)
	}
}

// A start and a stop each have their records, in order, with the VM's
// generation (S09-11-audit). The first start makes the data disk first.
func TestAuditStartAndStopRecorded(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	startAndStop(t, h, m, vm.SlotWork)
	waitStopRecord(t, h)
	want := []string{"data disk made", "vm start", "vm running", "vm stop", "vm stopped"}
	if got := h.audit.Messages(); !slices.Equal(got, want) {
		t.Errorf("records = %q, want %q", got, want)
	}
	checkRecord(t, h, "vm start", string(vm.RuleStart), statusSuccess)
	checkRecord(t, h, "vm stopped", string(vm.RuleStop), statusSuccess)
	if r := h.audit.Find("vm start")[0].Fields().Resources; r == nil || r.CPUCount == 0 || r.MemoryMiB == 0 {
		t.Errorf("start record resources = %+v", r)
	}
	if !h.audit.Find("vm stopped")[0].Stop {
		t.Error("the stopped record isn't marked as a stop record")
	}
}

// A VM that stops without a stop request has a stop record that is a
// failure, with vm-stopped-unexpectedly.
func TestAuditUnexpectedStopRecorded(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	h.provider.Last().Publish(1, vmdv1.State_STATE_STOPPED, nil)
	waitStopped(t, m, vm.SlotWork)
	waitStopRecord(t, h)
	checkRecord(t, h, "vm stopped", string(vm.RuleStoppedUnexpectedly), statusFailure)
}

// The writer's refusal refuses a start before any step: no generation,
// no instance, and the refusal's rule logged (S09-11-audit, "Free space
// and failed writes").
func TestAuditRefusesStart(t *testing.T) {
	for _, rule := range []string{audit.RuleFreeSpace, audit.RuleWriteFailed} {
		t.Run(rule, func(t *testing.T) {
			h := newHarness(t)
			m := h.manager()
			h.audit.Refuse(rule, "the audit log can't take records")
			refusal(t, m.Start(t.Context(), vm.SlotWork), vm.Rule(rule))
			if n := len(h.provider.Instances()); n != 0 {
				t.Errorf("%d instances opened", n)
			}
			if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped || s.Generation != 0 {
				t.Errorf("status = %+v", s)
			}
			if !strings.Contains(h.logs.String(), `"rule":"`+rule+`"`) {
				t.Errorf("the refusal isn't logged with %s", rule)
			}
			if len(h.audit.Records()) != 0 {
				t.Errorf("records = %q", h.audit.Messages())
			}
		})
	}
}

// A start whose record can't be written never reaches the provider, and
// the slot can start again once records are written.
func TestAuditStartRecordNotWritten(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	h.audit.RefuseWrites(audit.RuleWriteFailed, "the record wasn't written")
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), vm.Rule(audit.RuleWriteFailed))
	if r.Generation != 1 {
		t.Errorf("generation = %d, want the start's", r.Generation)
	}
	if n := len(h.provider.Instances()); n != 0 {
		t.Errorf("%d instances opened", n)
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
	h.audit.RefuseWrites("", "")
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatalf("start after the writer recovered: %v", err)
	}
}

// The writer's forced stop closes the instance without a stop request,
// and the stop's records carry the writer's rule.
func TestAuditForceStop(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	inst := h.provider.Last()
	m.ForceStop(string(vm.SlotWork), audit.RuleWriteFailed)
	waitStopped(t, m, vm.SlotWork)
	if !inst.Closed() || len(inst.Stops()) != 0 {
		t.Errorf("closed %v, stops %v: want closed without a stop request", inst.Closed(), inst.Stops())
	}
	waitStopRecord(t, h)
	checkRecord(t, h, "vm stop", audit.RuleWriteFailed, statusSuccess)
	checkRecord(t, h, "vm stopped", audit.RuleWriteFailed, statusSuccess)
	if !strings.Contains(h.logs.String(), `"decision":"stop-vm"`) {
		t.Error("the forced stop isn't logged")
	}
	// A slot without a run is left as it is.
	m.ForceStop(string(vm.SlotIsolated), audit.RuleWriteFailed)
	m.ForceStop("nonsense", audit.RuleWriteFailed)
	if n := len(h.provider.Instances()); n != 1 {
		t.Errorf("%d instances", n)
	}
}

// A provider can't pass on a rule of the audit writer's.
func TestAuditRuleFromProviderRefused(t *testing.T) {
	h := newHarness(t)
	h.provider.OnStart = func(_ context.Context, _ *vmtest.Instance, _ *vmdv1.StartRequest) error {
		return &vm.ProviderRefusal{Rule: audit.RuleWriteFailed}
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleProviderFailed)
	waitStopRecord(t, h)
	checkRecord(t, h, "vm stopped", string(vm.RuleProviderFailed), statusFailure)
}
