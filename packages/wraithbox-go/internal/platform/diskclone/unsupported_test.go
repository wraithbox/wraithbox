//go:build linux || windows

package diskclone

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestUnsupported is the platform contract test for an OS without disk
// cloning: Clone fails with an error that wraps errors.ErrUnsupported
// and makes nothing (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func TestUnsupported(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "tools.img.partial")
	if err := (Cloner{}).Clone(filepath.Join(dir, "src.img"), dst); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Clone: %v, want errors.ErrUnsupported", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
		t.Fatalf("Clone made %v", m)
	}
}
