//go:build linux || windows

package guestsched

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where cron
// and at aren't checked yet: CheckAllow, Scan and Find fail at once
// with an error that wraps errors.ErrUnsupported
// (S11-verification-and-spikes, "Platform contract tests",
// S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	if err := CheckAllow(); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("CheckAllow = %v, want errors.ErrUnsupported", err)
	}
	if _, err := Scan(); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Scan = %v, want errors.ErrUnsupported", err)
	}
	if _, err := (System{}).Find("wbp10000", 10000); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Find = %v, want errors.ErrUnsupported", err)
	}
}
