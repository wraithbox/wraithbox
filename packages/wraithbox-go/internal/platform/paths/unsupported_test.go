//go:build linux || windows

package paths

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where the
// paths aren't built: every exported function fails with an error that
// wraps errors.ErrUnsupported (S11-verification-and-spikes, "Platform
// contract tests", S12-01-platform-interfaces, "Unimplemented platforms
// fail clearly").
func TestUnsupported(t *testing.T) {
	calls := map[string]func() error{
		"Current": func() error { _, err := Current(nil); return err },
		"Real":    func() error { _, err := Paths{}.Real(); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
}
