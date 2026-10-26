package projectuser_test

import (
	"math"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// TestRange: the range starts at 10000 and ends below 2^31, so below
// macOS's nobody (S06-03-image-updates, "Allocation").
func TestRange(t *testing.T) {
	if projectuser.FirstID != 10000 {
		t.Errorf("FirstID = %d, want 10000", projectuser.FirstID)
	}
	if projectuser.LastID >= math.MaxInt32 || projectuser.LastID < projectuser.FirstID {
		t.Errorf("LastID = %d, want from FirstID to below 2^31", projectuser.LastID)
	}
	for _, id := range []uint32{projectuser.FirstID - 1, projectuser.LastID + 1, math.MaxUint32 - 1} {
		if projectuser.InRange(id) {
			t.Errorf("InRange(%d) = true", id)
		}
	}
}

// TestHomeRootIsTheMountPoint: the homes are under wb-guestd's data
// mount point.
func TestHomeRootIsTheMountPoint(t *testing.T) {
	if projectuser.HomeRoot != guestd.HomePath {
		t.Errorf("HomeRoot = %q, wb-guestd mounts at %q", projectuser.HomeRoot, guestd.HomePath)
	}
}

func TestNames(t *testing.T) {
	if got := projectuser.UserName(10000); got != "wbp10000" {
		t.Errorf("UserName(10000) = %q", got)
	}
	if got := len(projectuser.UserName(math.MaxUint32)); got != projectuser.MaxNameLen {
		t.Errorf("longest name is %d bytes, MaxNameLen is %d", got, projectuser.MaxNameLen)
	}
	if got := projectuser.HomePath("wbp10000"); got != "/private/var/wbhomes/wbp10000" {
		t.Errorf("HomePath = %q", got)
	}
}

func TestGeneratedUID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		g := projectuser.NewGeneratedUID()
		if !projectuser.ValidGeneratedUID(g) {
			t.Fatalf("NewGeneratedUID() = %q, not in the form", g)
		}
		if seen[g] {
			t.Fatalf("NewGeneratedUID() repeated %q", g)
		}
		seen[g] = true
	}
	for _, bad := range []string{
		"",
		"0f1e2d3c-4b5a-4978-8796-a5b4c3d2e1f0",  // lowercase
		"0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0",  // version 6
		"0F1E2D3C-4B5A-4978-C796-A5B4C3D2E1F0",  // variant
		"0F1E2D3C-4B5A-4978-8796-A5B4C3D2E1F",   // short
		"0F1E2D3C-4B5A-4978-8796-A5B4C3D2E1F0A", // long
		"0F1E2D3C+4B5A-4978-8796-A5B4C3D2E1F0",  // separator
		"0F1E2D3C-4B5A-4978-8796-A5B4C3D2E1G0",  // not hex
	} {
		if projectuser.ValidGeneratedUID(bad) {
			t.Errorf("ValidGeneratedUID(%q) = true", bad)
		}
	}
	if !projectuser.ValidGeneratedUID("0F1E2D3C-4B5A-4978-8796-A5B4C3D2E1F0") {
		t.Error("a valid GeneratedUID is refused")
	}
}
