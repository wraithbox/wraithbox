package hwkey

import (
	"errors"
	"testing"
)

// TestUnsupported: until each backend is built, Generate and Restore
// fail on every platform with an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func TestUnsupported(t *testing.T) {
	p := Open()
	if k, err := p.Generate(t.Context()); k != nil || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Generate = %v, %v, want ErrUnsupported", k, err)
	}
	if k, err := p.Restore(t.Context(), []byte{1, 2, 3}); k != nil || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Restore = %v, %v, want ErrUnsupported", k, err)
	}
}
