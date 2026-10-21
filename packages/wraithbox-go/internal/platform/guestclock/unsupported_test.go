//go:build linux || windows

package guestclock

import (
	"errors"
	"testing"
	"time"
)

// TestUnsupported is the platform contract test for an OS where setting
// the clock isn't built: Step and Slew fail at once with an error that
// wraps errors.ErrUnsupported (S11-verification-and-spikes, "Platform
// contract tests", S12-01-platform-interfaces, "Unimplemented platforms
// fail clearly").
func TestUnsupported(t *testing.T) {
	if err := Step(time.Now()); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Step = %v, want errors.ErrUnsupported", err)
	}
	if err := Slew(time.Millisecond); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Slew = %v, want errors.ErrUnsupported", err)
	}
}
