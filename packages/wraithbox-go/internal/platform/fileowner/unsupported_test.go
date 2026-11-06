//go:build windows

package fileowner

import (
	"errors"
	"os"
	"testing"
)

// TestUnsupported is the platform contract test for an OS without file
// owners: UID fails with an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UID(info); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("UID: %v, want errors.ErrUnsupported", err)
	}
}
