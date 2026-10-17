package vm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// The VM manager's data disk steps (C11-host-daemons, "Data disk
// steps"), with the real data disk manager and a fake wb-diskfmt.

const (
	uuid1 = "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"
	uuid2 = "11111111-2222-3333-4444-555555555555"
)

func (h *harness) dataDisk(slot vm.Slot) string {
	return filepath.Join(h.bundle(slot), vmbundle.DataDiskName)
}

// TestStartMakesDataDisk: the first start makes and records a data
// disk before the provider sees the VM, and records the first start.
func TestStartMakesDataDisk(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := h.db.DataDiskRecord(t.Context(), "macos-work")
	if err != nil || !ok || !rec.Formatted || rec.VolumeUUID != uuid1 || rec.FirstStarted.IsZero() {
		t.Fatalf("record %+v (%t, %v)", rec, ok, err)
	}
	if _, err := os.Lstat(h.dataDisk(vm.SlotWork)); err != nil {
		t.Fatal(err)
	}
}

// TestStartRefusedUnrecorded: a file at the final name that state.db
// doesn't record refuses the start before any provider instance, and
// the file is kept.
func TestStartRefusedUnrecorded(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.bundle(vm.SlotWork), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.dataDisk(vm.SlotWork), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	r := refusal(t, m.Start(t.Context(), vm.SlotWork), vm.Rule(datadisk.RuleUnrecorded))
	if !strings.Contains(r.Message, "wb vm reset-data work") {
		t.Errorf("message %q doesn't name the reset", r.Message)
	}
	if b, _ := os.ReadFile(h.dataDisk(vm.SlotWork)); string(b) != "kept" {
		t.Errorf("the file holds %q", b)
	}
	if len(h.provider.Instances()) != 0 {
		t.Error("a provider instance was opened")
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase %s, want stopped", s.Phase)
	}
}

// TestResetData: refused while the VM runs, then after the stop the
// old disk is replaced with a new one with a new UUID.
func TestResetData(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	_, err := m.ResetData(t.Context(), vm.SlotWork)
	refusal(t, err, vm.RuleRunning)
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	res, err := m.ResetData(t.Context(), vm.SlotWork)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldVolumeUUID != uuid1 || res.NewVolumeUUID != uuid2 {
		t.Errorf("result %+v", res)
	}
	rec, _, _ := h.db.DataDiskRecord(t.Context(), "macos-work")
	if rec.VolumeUUID != uuid2 || !rec.Formatted || !rec.FirstStarted.IsZero() {
		t.Errorf("record %+v", rec)
	}
	if !strings.Contains(h.logs.String(), `"old_volume_uuid":"`+uuid1+`"`) {
		t.Error("the reset line doesn't hold the old UUID")
	}
	_, err = m.ResetData(t.Context(), "build")
	refusal(t, err, vm.RuleSlotUnknown)
}

// TestStartRefusedWhileResetting: a reset holds the slot's start lock.
func TestStartRefusedWhileResetting(t *testing.T) {
	h := newHarness(t)
	h.shim.Block = make(chan struct{})
	m := h.manager()
	done := make(chan error, 1)
	go func() {
		_, err := m.ResetData(context.WithoutCancel(t.Context()), vm.SlotIsolated)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.shim.Starts()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	refusal(t, m.Start(t.Context(), vm.SlotIsolated), vm.RuleResetting)
	_, err := m.ResetData(t.Context(), vm.SlotIsolated)
	refusal(t, err, vm.RuleResetting)
	close(h.shim.Block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := m.Start(t.Context(), vm.SlotIsolated); err != nil {
		t.Fatal(err)
	}
}

// TestProviderDataDiskRuleRefused: a provider that returns a data disk
// rule is refused with vm-provider-failed, so such a rule always comes
// from the data disk step.
func TestProviderDataDiskRuleRefused(t *testing.T) {
	for _, rule := range []string{string(datadisk.RuleMissing), "diskfmt-backing-path"} {
		h := newHarness(t)
		h.provider.OnStart = func(context.Context, *vmtest.Instance, *vmdv1.StartRequest) error {
			return &vm.ProviderRefusal{Rule: rule, Detail: "from wb-vmd"}
		}
		m := h.manager()
		refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleProviderFailed)
	}
}
