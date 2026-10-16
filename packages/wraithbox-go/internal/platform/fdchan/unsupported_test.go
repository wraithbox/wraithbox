//go:build linux || windows

package fdchan

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestUnsupported is the platform contract test for an OS where the
// channel isn't built: every exported function fails with an error that
// wraps errors.ErrUnsupported (S11-verification-and-spikes, "Platform
// contract tests", S12-01-platform-interfaces, "Unimplemented platforms
// fail clearly"). Message.Close isn't platform code and isn't listed.
func TestUnsupported(t *testing.T) {
	var c Channel
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"Socketpair": func() error { _, _, err := Socketpair(4096); return err },
		"New":        func() error { _, err := New(f, 4096); return err },
		"SocketType": func() error { _, _, err := SocketType(f); return err },
		"Send":       func() error { return c.Send([]byte("x"), nil) },
		"Recv":       func() error { _, err := c.Recv(); return err },
		"SetReadDeadline": func() error {
			return c.SetReadDeadline(time.Now())
		},
		"SetWriteDeadline": func() error {
			return c.SetWriteDeadline(time.Now())
		},
		"Close": c.Close,
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("New left its file open: Close = %v", err)
	}
}
