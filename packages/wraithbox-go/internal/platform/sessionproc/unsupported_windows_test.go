package sessionproc_test

import (
	"errors"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// TestUnsupported is the platform contract test for windows, whose
// guests are later: every call fails with an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func TestUnsupported(t *testing.T) {
	calls := map[string]func() error{
		"OpenPTY":      func() error { _, _, err := sessionproc.OpenPTY(sessionproc.Cred{}, 24, 80); return err },
		"StartHelper":  func() error { _, err := sessionproc.StartHelper(sessionproc.HelperSpec{}); return err },
		"StartProgram": func() error { _, err := sessionproc.StartProgram(sessionproc.ProgramSpec{}); return err },
		"Wait":         func() error { _, err := sessionproc.Wait(100); return err },
		"SignalGroup":  func() error { return sessionproc.SignalGroup(100, sessionproc.SigHup) },
		"NoCore":       sessionproc.NoCore,
		"UserTempDir":  func() error { _, err := sessionproc.UserTempDir(t.Context()); return err },
		"ByUID":        func() error { _, err := sessionproc.System{}.ByUID(10000); return err },
		"Kill":         func() error { return sessionproc.System{}.Kill(sessionproc.Proc{PID: 100}) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
}
