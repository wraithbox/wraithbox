package launch_test

import (
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/launch"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// TestDiskfmtEntries: each wb-diskfmt entry's WB_DISKFMT_SLOT value and
// its bundle parameter belong to the same slot, its name says that
// slot, and its environment holds nothing else (C11-host-daemons,
// "Launcher table"; S04-04-vm-processes, "`wb-diskfmt`").
func TestDiskfmtEntries(t *testing.T) {
	seen := map[string]int{}
	for _, e := range launch.Entries {
		if e.Program != launch.DiskfmtProgram {
			continue
		}
		slot := checkEntry(t, e)
		seen[slot]++
	}
	for _, s := range vmbundle.Slots() {
		got, ok := launch.Diskfmt(s)
		if seen[s] != 1 || !ok || got.Slot != s {
			t.Errorf("%d wb-diskfmt entries for %s, want 1", seen[s], s)
		}
	}
	if _, ok := launch.Diskfmt("build"); ok {
		t.Error("an entry for the build slot")
	}
}

// checkEntry checks one wb-diskfmt entry, and returns the slot its
// variable names.
func checkEntry(t *testing.T, e launch.Entry) string {
	t.Helper()
	if len(e.Env) != 1 {
		t.Errorf("%s: environment %v, want only %s", e.Name, e.Env, diskfmt.SlotEnv)
		return ""
	}
	slot, ok := strings.CutPrefix(e.Env[0], diskfmt.SlotEnv+"=")
	if !ok || !vmbundle.ValidSlot(slot) {
		t.Errorf("%s: environment %v doesn't name a slot", e.Name, e.Env)
		return ""
	}
	if e.Slot != slot || e.BundleParam != vmbundle.Rel(slot) || e.Name != "wb-diskfmt-"+slot {
		t.Errorf("%s: slot %s, variable %s, bundle %s: not one slot", e.Name, e.Slot, slot, e.BundleParam)
	}
	return slot
}
