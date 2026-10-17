package platform

import (
	"runtime"
	"strings"
	"testing"
)

// TestReadRelease reads this Mac's release. An arm64 test binary runs
// only on Apple Silicon.
func TestReadRelease(t *testing.T) {
	r, err := ReadRelease()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOARCH == "arm64" && !r.AppleSilicon {
		t.Errorf("AppleSilicon = false on an arm64 test binary")
	}
	if major, _, _ := strings.Cut(r.Version, "."); major == "" {
		t.Errorf("Version = %q, want a product version", r.Version)
	}
}
