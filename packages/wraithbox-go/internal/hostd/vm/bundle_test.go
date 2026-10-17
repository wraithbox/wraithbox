package vm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noTemp(t *testing.T, bundle string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(bundle, "tools.img.partial")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a temporary file is left: %v", err)
	}
}

// The bundle directory is created mode 700 when it is missing.
func TestBundleCreated(t *testing.T) {
	h := newHarness(t)
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotIsolated); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(h.bundle(vm.SlotIsolated))
	if err != nil || !info.IsDir() {
		t.Fatalf("bundle = %v, %v", info, err)
	}
}

// C11-host-daemons, "VM manager": a bundle path that is a file is
// refused, before the tools disk or the provider.
func TestC11BundleFileRefused(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Dir(h.bundle(vm.SlotWork)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.bundle(vm.SlotWork), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleBundleNotDirectory)
	if h.cloner.Calls() != 0 || len(h.provider.Instances()) != 0 {
		t.Error("a step after the bundle check ran")
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
}

// C11-host-daemons, "VM manager": tools.img is replaced before each
// start, from the installed bundle's guest tools disk.
func TestC11ToolsDiskReplacedBeforeEachStart(t *testing.T) {
	h := newHarness(t)
	var seen []string
	h.provider.OnStart = func(_ context.Context, i *vmtest.Instance, req *vmdv1.StartRequest) error {
		seen = append(seen, readFile(t, filepath.Join(h.bundle(i.Slot()), vm.ToolsDiskName)))
		i.Publish(req.GetGeneration(), vmdv1.State_STATE_RUNNING, nil)
		return nil
	}
	m := h.manager()
	for _, version := range []string{"tools v1", "tools v2"} {
		h.writeTools(version)
		if err := m.Start(t.Context(), vm.SlotWork); err != nil {
			t.Fatal(err)
		}
		// What wb-vmd wrote into its copy is gone at the next start.
		if err := os.WriteFile(filepath.Join(h.bundle(vm.SlotWork), vm.ToolsDiskName), []byte("changed by wb-vmd"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.Stop(t.Context(), vm.SlotWork); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != "tools v1" || seen[1] != "tools v2" {
		t.Errorf("the provider saw %q", seen)
	}
	noTemp(t, h.bundle(vm.SlotWork))
}

// C11-host-daemons, "VM manager": a temporary file left by an earlier
// crash is deleted.
func TestC11LeftoverTempDeleted(t *testing.T) {
	h := newHarness(t)
	bundle := h.bundle(vm.SlotWork)
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "tools.img.partial"), []byte("half a clone"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	if err := m.Start(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	noTemp(t, bundle)
	if got := readFile(t, filepath.Join(bundle, vm.ToolsDiskName)); got != "tools v1" {
		t.Errorf("tools.img = %q", got)
	}
}

// C11-host-daemons, "VM manager": a clone failure refuses the start
// with tools-disk-clone-failed and leaves no temporary file.
func TestC11CloneFailureRefuses(t *testing.T) {
	h := newHarness(t)
	h.cfg.Cloner = vmtest.ClonerFunc(func(_, dst string) error {
		if err := os.WriteFile(dst, []byte("half"), 0o600); err != nil {
			return err
		}
		return errors.New("clonefile: no space left on device")
	})
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleToolsDiskCloneFailed)
	noTemp(t, h.bundle(vm.SlotWork))
	if len(h.provider.Instances()) != 0 {
		t.Error("the provider was opened after the clone failed")
	}
	if s := m.Status(vm.SlotWork); s.Phase != vm.PhaseStopped {
		t.Errorf("phase = %s", s.Phase)
	}
}

// A missing installed tools disk refuses the start.
func TestMissingToolsImageRefuses(t *testing.T) {
	h := newHarness(t)
	if err := os.Remove(h.tools); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleToolsDiskCloneFailed)
	if h.cloner.Calls() != 0 {
		t.Error("the cloner ran without a source")
	}
}

// A directory at tools.img can't be replaced: the start is refused and
// no temporary file is left.
func TestToolsDiskDirectoryRefuses(t *testing.T) {
	h := newHarness(t)
	bundle := h.bundle(vm.SlotWork)
	if err := os.MkdirAll(filepath.Join(bundle, vm.ToolsDiskName, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	m := h.manager()
	refusal(t, m.Start(t.Context(), vm.SlotWork), vm.RuleToolsDiskCloneFailed)
	noTemp(t, bundle)
}
