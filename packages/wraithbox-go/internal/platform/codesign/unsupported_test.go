//go:build linux || windows

package codesign

import (
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS without code
// signatures of the macOS kind: both readers fail at once with an error
// that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	if _, err := ReadTeam(t.Context(), "x"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("ReadTeam = %v, want errors.ErrUnsupported", err)
	}
	if _, err := ReadProfile(t.Context(), "x"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("ReadProfile = %v, want errors.ErrUnsupported", err)
	}
}
