//go:build windows

package volspace

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestUnsupported is the platform contract test for an OS without
// volume space: every call fails with an error that wraps
// errors.ErrUnsupported, and Allocate makes nothing
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	dir := t.TempDir()
	if _, err := Stat(dir); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Stat: %v, want errors.ErrUnsupported", err)
	}
	if _, err := Allocated(dir); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Allocated: %v, want errors.ErrUnsupported", err)
	}
	if err := Allocate(filepath.Join(dir, "reserve"), 1<<20); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Allocate: %v, want errors.ErrUnsupported", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
		t.Fatalf("Allocate made %v", m)
	}
}
