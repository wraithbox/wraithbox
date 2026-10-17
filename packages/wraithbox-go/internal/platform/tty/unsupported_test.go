//go:build linux || windows

package tty

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where the
// controlling terminal isn't built: Open fails with an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func TestUnsupported(t *testing.T) {
	if _, err := Open(); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Open: %v, want errors.ErrUnsupported", err)
	}
}
