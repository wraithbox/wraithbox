//go:build linux || windows

package packetlink

import (
	"errors"
	"os"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where the
// transport isn't built: every exported function fails with an error
// that wraps errors.ErrUnsupported (S11-verification-and-spikes,
// "Platform contract tests", S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func TestUnsupported(t *testing.T) {
	var tr Transport
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"New":        func() error { _, err := New(f, MaxMTU); return err },
		"Buffers":    func() error { _, _, err := tr.Buffers(); return err },
		"ReadFrame":  func() error { _, err := tr.ReadFrame(make([]byte, 16)); return err },
		"WriteFrame": func() error { return tr.WriteFrame(make([]byte, 16)) },
		"Close":      tr.Close,
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
