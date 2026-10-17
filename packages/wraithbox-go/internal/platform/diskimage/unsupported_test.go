//go:build linux || windows

package diskimage

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where the
// data disk format isn't built: every call fails with an error that
// wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	files, err := NewFiles()
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("NewFiles: %v", err)
	}
	if _, err := NewTool(nil); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("NewTool: %v", err)
	}
	tool := &Tool{}
	calls := map[string]func() error{
		"Create":      func() error { _, err := files.Create("x", 1); return err },
		"Lstat":       func() error { _, err := files.Lstat("x"); return err },
		"Unlink":      func() error { return files.Unlink("x") },
		"RenameExcl":  func() error { return files.RenameExcl("x", "y") },
		"Attach":      func() error { _, err := tool.Attach(t.Context(), "x"); return err },
		"BackingPath": func() error { _, err := tool.BackingPath(t.Context(), "disk5"); return err },
		"Erase":       func() error { return tool.Erase(t.Context(), "disk5") },
		"VolumeUUID":  func() error { _, err := tool.VolumeUUID(t.Context(), "disk5"); return err },
		"Detach":      func() error { return tool.Detach(t.Context(), "disk5") },
		"Allocated":   func() error { _, err := Allocated("x"); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
}
