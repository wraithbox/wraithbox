package vm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// The start lock `wb vm reset-ca` takes (S05-03-management-commands,
// "`wb vm reset-ca`").

const resetCA = "wb vm reset-ca work"

// TestWhileStoppedRefusedRunning: fn doesn't run while the VM runs, and
// the refusal is vm-running.
func TestWhileStoppedRefusedRunning(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	ran := false
	err := m.WhileStopped(t.Context(), vm.SlotWork, resetCA, "reset the work VM's CA", func(context.Context) error {
		ran = true
		return nil
	})
	r := refusal(t, err, vm.RuleRunning)
	if ran {
		t.Error("fn ran while the VM runs")
	}
	if !strings.Contains(r.Message, "reset the work VM's CA") {
		t.Errorf("message %q", r.Message)
	}
	err = m.WhileStopped(t.Context(), "build", resetCA, "x", func(context.Context) error { return nil })
	refusal(t, err, vm.RuleSlotUnknown)
}

// TestWhileStoppedHoldsStartLock: while fn runs, a start and a data
// disk reset are refused with vm-resetting, which names the command.
// After fn the VM starts.
func TestWhileStoppedHoldsStartLock(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	want := errors.New("from fn")
	err := m.WhileStopped(t.Context(), vm.SlotWork, resetCA, "reset the work VM's CA", func(ctx context.Context) error {
		r := refusal(t, m.Start(ctx, vm.SlotWork), vm.RuleResetting)
		if !strings.Contains(r.Message, resetCA) {
			t.Errorf("message %q doesn't name %s", r.Message, resetCA)
		}
		_, err := m.ResetData(ctx, vm.SlotWork)
		refusal(t, err, vm.RuleResetting)
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err %v, want fn's error", err)
	}
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
}
