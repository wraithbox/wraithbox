//go:build conformance && darwin

package guestclock

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestclock"
)

// inGuestAsRoot skips the test unless it runs as root in a VM.
func inGuestAsRoot(t *testing.T) {
	t.Helper()
	if vmm, err := unix.SysctlUint32("kern.hv_vmm_present"); err != nil || vmm != 1 {
		t.Skipf("not in a guest: kern.hv_vmm_present %d, %v", vmm, err)
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root, as wb-guestd runs")
	}
}

// wall returns the wall clock without the monotonic reading, so
// differences between two readings follow a step.
func wall() time.Time { return time.Now().Round(0) }

// TestStep: a step moves the wall clock at once, forward and back.
func TestStep(t *testing.T) {
	inGuestAsRoot(t)
	for _, by := range []time.Duration{5 * time.Second, -5 * time.Second} {
		target := wall().Add(by)
		if err := guestclock.Step(target); err != nil {
			t.Fatalf("Step by %v: %v", by, err)
		}
		if off := wall().Sub(target); off < 0 || off > 500*time.Millisecond {
			t.Fatalf("Step by %v: the clock is %v from the target", by, off)
		}
	}
}

// TestSlew: a slew is accepted and the kernel has it pending, for both
// signs. The check cancels it after.
func TestSlew(t *testing.T) {
	inGuestAsRoot(t)
	defer func() {
		var zero unix.Timeval
		if err := unix.Adjtime(&zero, nil); err != nil {
			t.Errorf("cancel the slew: %v", err)
		}
	}()
	for _, by := range []time.Duration{500 * time.Millisecond, -500 * time.Millisecond} {
		if err := guestclock.Slew(by); err != nil {
			t.Fatalf("Slew by %v: %v", by, err)
		}
		var left unix.Timeval
		if err := unix.Adjtime(nil, &left); err != nil {
			t.Fatalf("adjtime: %v", err)
		}
		pending := time.Duration(left.Nano())
		if pending == 0 || (pending > 0) != (by > 0) || pending.Abs() > by.Abs() {
			t.Fatalf("Slew by %v: %v pending", by, pending)
		}
	}
}
