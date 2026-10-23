package vm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/diskclone"
)

// C11-host-daemons, "VM manager", with the APFS clone of
// internal/platform/diskclone in place of the copying cloner: a
// symbolic link at tools.img and a leftover temporary file are replaced,
// the link's target is unchanged, an old copy with mode 000 is replaced
// without being opened (SEC15-no-guest-disk-mount), and each start
// clones the installed disk again.
func TestC11ToolsDiskAPFSClone(t *testing.T) {
	h := newHarness(t)
	h.cfg.Cloner = diskclone.Cloner{}
	bundle := h.bundle(vm.SlotWork)
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "host-file")
	if err := os.WriteFile(outside, []byte("host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(bundle, vm.ToolsDiskName)
	if err := os.Symlink(outside, disk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "tools.img.partial"), []byte("crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	startAndStop(t, h, m, vm.SlotWork)
	info, err := os.Lstat(disk)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("tools.img = %v, %v; want a regular file", info, err)
	}
	if got := readFile(t, disk); got != "tools v1" {
		t.Errorf("tools.img = %q", got)
	}
	if got := readFile(t, outside); got != "host data" {
		t.Errorf("the link's target changed to %q", got)
	}
	noTemp(t, bundle)

	if err := os.Chmod(disk, 0); err != nil {
		t.Fatal(err)
	}
	h.writeTools("tools v2")
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, disk); got != "tools v2" {
		t.Errorf("tools.img after the second start = %q", got)
	}
	noTemp(t, bundle)
}
