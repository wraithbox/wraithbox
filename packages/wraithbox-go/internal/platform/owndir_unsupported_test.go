//go:build linux || windows

package platform

import (
	"errors"
	"testing"
)

// TestCheckOwnDirUnsupported is the platform contract test of an OS
// where the owner check isn't built (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestCheckOwnDirUnsupported(t *testing.T) {
	if err := CheckOwnDir(t.TempDir()); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("CheckOwnDir: %v, want errors.ErrUnsupported", err)
	}
}
