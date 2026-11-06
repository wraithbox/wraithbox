package fdchan

import (
	"os"
	"testing"
)

// TestFirstOpenFrom: an open descriptor is found from its number on,
// and isn't once it is closed.
func TestFirstOpenFrom(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	high := int(w.Fd())
	got, err := FirstOpenFrom(high)
	if err != nil || got != high {
		t.Fatalf("FirstOpenFrom(%d) = %d, %v", high, got, err)
	}
	_ = w.Close()
	// Another test may open a descriptor meanwhile, but not this one.
	if got, err := FirstOpenFrom(high); err != nil || got == high {
		t.Fatalf("FirstOpenFrom(%d) after the close = %d, %v", high, got, err)
	}
}
