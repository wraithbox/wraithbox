//go:build linux || windows

package guestmount_test

import (
	"errors"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

// TestUnsupported is the platform contract test for an OS where the
// guest's data mount isn't built: every call fails with an error that
// wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	s := &guestmount.System{}
	calls := map[string]func() error{
		"Info":    func() error { _, _, err := s.Info(t.Context(), "x"); return err },
		"Unmount": func() error { return s.Unmount(t.Context(), "disk5s1") },
		"Mount":   func() error { return s.Mount(t.Context(), "disk5s1", "/x") },
		"Statfs":  func() error { _, err := s.Statfs("/"); return err },
		"Lstat":   func() error { _, err := s.Lstat("/"); return err },
		"Mkdir":   func() error { return s.Mkdir("x") },
		"Claim":   func() error { return s.Claim("x") },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
}
