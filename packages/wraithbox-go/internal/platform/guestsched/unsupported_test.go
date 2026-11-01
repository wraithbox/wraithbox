//go:build linux || windows

package guestsched

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where cron
// and at aren't closed yet: Lock, Sweep and Clear fail at once with an error
// that wraps errors.ErrUnsupported (S11-verification-and-spikes,
// "Platform contract tests", S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	if err := Lock(); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Lock = %v, want errors.ErrUnsupported", err)
	}
	if _, err := Sweep(); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Sweep = %v, want errors.ErrUnsupported", err)
	}
	if _, err := (System{}).Clear("wbp10000", 10000); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Clear = %v, want errors.ErrUnsupported", err)
	}
}
