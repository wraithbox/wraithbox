//go:build unix

package vm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// Symbolic links need privileges on Windows, and Windows has no mode
// bits, so these cases run on Unix hosts only.

// C11-host-daemons, "VM manager": a bundle path that is a symbolic link
// is refused and never followed, even to a directory.
func TestC11BundleSymlinkRefused(t *testing.T) {
	h := newHarness(t)
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(h.bundle(vm.SlotWork)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, h.bundle(vm.SlotWork)); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleBundleNotDirectory)
	entries, err := os.ReadDir(other)
	if err != nil || len(entries) != 0 {
		t.Errorf("the link's target was written: %v, %v", entries, err)
	}
	if len(h.provider.Instances()) != 0 {
		t.Error("the provider was opened")
	}
}

// C11-host-daemons, "VM manager": a symbolic link at tools.img to a file
// outside the bundle is replaced, and the target is unchanged.
func TestC11ToolsDiskSymlinkReplaced(t *testing.T) {
	h := newHarness(t)
	bundle := h.bundle(vm.SlotWork)
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "host-file")
	if err := os.WriteFile(outside, []byte("host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bundle, vm.ToolsDiskName)); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(bundle, vm.ToolsDiskName))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("tools.img = %v, %v; want a regular file", info, err)
	}
	if got := readFile(t, filepath.Join(bundle, vm.ToolsDiskName)); got != "tools v1" {
		t.Errorf("tools.img = %q", got)
	}
	if got := readFile(t, outside); got != "host data" {
		t.Errorf("the link's target changed to %q", got)
	}
}

// A symbolic link left at the temporary name is deleted, not followed.
func TestLeftoverTempSymlinkDeleted(t *testing.T) {
	h := newHarness(t)
	bundle := h.bundle(vm.SlotWork)
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "host-file")
	if err := os.WriteFile(outside, []byte("host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bundle, "tools.img.partial")); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	noTemp(t, bundle)
	if got := readFile(t, outside); got != "host data" {
		t.Errorf("the link's target changed to %q", got)
	}
}

// C11-host-daemons, "VM manager": an old copy with mode 000 is still
// replaced, which shows it is never opened (SEC15-no-guest-disk-mount).
func TestC11OldCopyNeverOpened(t *testing.T) {
	h := newHarness(t)
	bundle := h.bundle(vm.SlotWork)
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(bundle, vm.ToolsDiskName)
	if err := os.WriteFile(old, []byte("old copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(old, 0); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, old); got != "tools v1" {
		t.Errorf("tools.img = %q", got)
	}
}

// A symbolic link as the installed tools disk is refused, not followed.
func TestToolsImageSymlinkRefused(t *testing.T) {
	h := newHarness(t)
	link := filepath.Join(t.TempDir(), "wb-guest-tools.img")
	if err := os.Symlink(h.tools, link); err != nil {
		t.Fatal(err)
	}
	h.cfg.ToolsImage = link
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleToolsDiskCloneFailed)
}
