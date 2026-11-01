//go:build linux || windows

package vmhandoff

import (
	"errors"
	"os"
	"testing"
)

// TestUnsupported is the platform contract test for an OS without the
// wb-vmd hand-off: every exported function fails with an error that
// wraps errors.ErrUnsupported (S11-verification-and-spikes, "Platform
// contract tests", S12-01-platform-interfaces, "Unimplemented platforms
// fail clearly").
func TestUnsupported(t *testing.T) {
	var r Receiver
	var p Pending
	var pn PendingNetwork
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"NewReceiver":   func() error { _, err := NewReceiver(f, nil); return err },
		"Expect":        func() error { _, err := r.Expect(1, 1, 1023); return err },
		"Run":           r.Run,
		"Receive":       r.Receive,
		"Close":         r.Close,
		"Wait":          func() error { _, err := p.Wait(t.Context()); return err },
		"ExpectNetwork": func() error { _, err := r.ExpectNetwork(1); return err },
		"WaitNetwork":   func() error { _, err := pn.Wait(t.Context()); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("NewReceiver left its file open: Close = %v", err)
	}
}
