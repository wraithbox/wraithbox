//go:build linux || windows

package localipc

import (
	"context"
	"errors"
	"testing"
)

// TestUnsupported is the platform contract test for an OS where local
// IPC isn't built: every exported function fails with an error that
// wraps errors.ErrUnsupported (S11-verification-and-spikes, "Platform
// contract tests", S12-01-platform-interfaces, "Unimplemented platforms
// fail clearly"). Listener and Conn come only from Listen and Dial, so
// their methods aren't listed.
func TestUnsupported(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	calls := map[string]func() error{
		"EnsureRunDir": func() error { return EnsureRunDir(nil, dir) },
		"CheckRunDir":  func() error { return CheckRunDir(nil, dir) },
		"Listen":       func() error { _, err := Listen(ctx, nil, dir, "x"); return err },
		"Dial":         func() error { _, err := Dial(ctx, nil, dir, "x"); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v, want errors.ErrUnsupported", name, err)
		}
	}
}
