//go:build !darwin || !cgo

package hwkey

import (
	"errors"
	"testing"
)

// TestUnsupported: where no backend is built, on linux, windows and a
// darwin build without cgo, Generate and Restore fail with an error
// that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly"). go:test runs it with
// CGO_ENABLED=0 on darwin.
func TestUnsupported(t *testing.T) {
	p := Open()
	if k, err := p.Generate(t.Context()); k != nil || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Generate = %v, %v, want ErrUnsupported", k, err)
	}
	if k, err := p.Restore(t.Context(), []byte{1, 2, 3}); k != nil || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Restore = %v, %v, want ErrUnsupported", k, err)
	}
}
