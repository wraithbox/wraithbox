//go:build linux || windows

package guestsock

import (
	"errors"
	"log/slog"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where the
// listener isn't built: Listen fails at once with an error that wraps
// errors.ErrUnsupported (S11-verification-and-spikes, "Platform contract
// tests", S12-01-platform-interfaces, "Unimplemented platforms fail
// clearly").
func TestUnsupported(t *testing.T) {
	l, err := Listen(t.Context(), slog.New(slog.DiscardHandler), Port)
	if !errors.Is(err, errors.ErrUnsupported) || l != nil {
		t.Fatalf("Listen = %v, %v; want nil, errors.ErrUnsupported", l, err)
	}
}
