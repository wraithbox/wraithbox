//go:build linux || windows

package guestusers_test

import (
	"errors"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// TestUnsupported is the platform contract test for an OS where the
// project users aren't built: every call fails with an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func TestUnsupported(t *testing.T) {
	s := &guestusers.System{}
	calls := map[string]func() error{
		"List": func() error { _, err := s.List(t.Context(), guestusers.Users, nil); return err },
		"Read": func() error { _, _, err := s.Read(t.Context(), guestusers.Users, "wbp10000"); return err },
		"Create": func() error {
			return s.Create(t.Context(), guestusers.Users, "wbp10000", guestusers.AttrShell, "/bin/zsh")
		},
		"OpenRoot": func() error { _, err := guestusers.OpenRoot("/"); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
}
