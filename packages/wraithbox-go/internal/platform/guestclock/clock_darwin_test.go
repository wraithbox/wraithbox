package guestclock

import (
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestNeedsRoot: on darwin, Step and Slew reach settimeofday and adjtime,
// which refuse a user other than root with EPERM. The test never runs as
// root, so it never sets the clock of the machine it runs on. The
// conformance suite checks a guest's clock as root (conformance/guestclock).
func TestNeedsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root: it would set this machine's clock")
	}
	if err := Step(time.Now()); !errors.Is(err, unix.EPERM) {
		t.Fatalf("Step = %v, want EPERM", err)
	}
	if err := Slew(-time.Millisecond); !errors.Is(err, unix.EPERM) {
		t.Fatalf("Slew = %v, want EPERM", err)
	}
}
