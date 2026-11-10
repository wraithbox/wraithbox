package appbundle

import (
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// TestStoreProgramsAreHelpers sees the executable of each program with
// store access be a helper of the layout with an access group, so the
// store's program check matches the bundle's CFBundleExecutable
// (S15-02-secret-store, "The group of each call").
func TestStoreProgramsAreHelpers(t *testing.T) {
	helpers := map[string]bool{}
	for _, p := range Programs {
		if p.Place == PlaceHelper && len(p.Roles) > 0 {
			helpers[p.Name] = true
		}
	}
	for _, p := range storeitem.Programs() {
		if !helpers[p.Executable()] {
			t.Errorf("%s: its executable %q is not a helper with a group in the layout", p, p.Executable())
		}
	}
}
