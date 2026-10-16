//go:build linux || windows

package selfsandbox

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where
// confinement isn't built: Confine fails with an error that wraps
// errors.ErrUnsupported, so a daemon that calls it exits
// (S11-verification-and-spikes, "Platform contract tests",
// S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	err := Confine(Profile{Name: "deny-all", Text: "(version 1)\n(deny default)\n"}, nil)
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Confine: %v, want errors.ErrUnsupported", err)
	}
}
