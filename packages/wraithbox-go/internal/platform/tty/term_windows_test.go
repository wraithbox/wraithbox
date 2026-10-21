package tty

import (
	"errors"
	"testing"
)

// TestRelayUnsupported is the platform contract test for windows,
// where the relay's calls aren't built: each fails with an error that
// wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestRelayUnsupported(t *testing.T) {
	if _, err := NewTerminal(0); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("NewTerminal: %v, want errors.ErrUnsupported", err)
	}
	if _, err := (&Terminal{}).MakeRaw(); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("MakeRaw: %v, want errors.ErrUnsupported", err)
	}
	if _, _, err := (&Terminal{}).Size(); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Size: %v, want errors.ErrUnsupported", err)
	}
	if _, err := Notify(make(chan Signal)); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Notify: %v, want errors.ErrUnsupported", err)
	}
	if err := StopSelf(); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("StopSelf: %v, want errors.ErrUnsupported", err)
	}
	if IsTerminal(0) {
		t.Error("IsTerminal(0) on windows, want false")
	}
}
