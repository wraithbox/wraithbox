//go:build linux || windows

package guesttools

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where the
// guest tools disk isn't built: every call fails at once with an error
// that wraps errors.ErrUnsupported (S11-verification-and-spikes,
// "Platform contract tests", S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func TestUnsupported(t *testing.T) {
	for name, err := range map[string]error{
		"CheckMount": CheckMount("/"),
		"PrepareDir": PrepareDir("/"),
		"Exec":       Exec("/"),
	} {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s = %v, want errors.ErrUnsupported", name, err)
		}
	}
}
