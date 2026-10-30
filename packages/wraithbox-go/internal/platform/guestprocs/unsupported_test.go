//go:build linux || windows

package guestprocs_test

import (
	"errors"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
)

// TestUnsupported is the platform contract test for an OS where the
// project users' processes aren't handled: every call fails with an
// error that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	s := &guestprocs.System{}
	calls := map[string]func() error{
		"ByUID":   func() error { _, err := s.ByUID(10000); return err },
		"InRange": func() error { _, err := s.InRange(10000, 10001); return err },
		"Signal":  func() error { return s.Signal(guestprocs.Proc{PID: 100}, guestprocs.Kill) },
		"Bootout": func() error { return s.Bootout(t.Context(), 10000) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
}
