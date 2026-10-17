package vmbundle_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

func TestPaths(t *testing.T) {
	data := filepath.Join("/", "data")
	for _, tc := range []struct{ slot, want string }{
		{vmbundle.SlotWork, filepath.Join(data, "vms", "macos-work.noindex")},
		{vmbundle.SlotIsolated, filepath.Join(data, "vms", "macos-isolated.noindex")},
	} {
		if got := vmbundle.Path(data, tc.slot); got != tc.want {
			t.Errorf("Path(%s) = %s, want %s", tc.slot, got, tc.want)
		}
		if !vmbundle.ValidSlot(tc.slot) {
			t.Errorf("ValidSlot(%s) = false", tc.slot)
		}
	}
	for _, s := range []string{"", "build", "Work", "work ", "../work"} {
		if vmbundle.ValidSlot(s) {
			t.Errorf("ValidSlot(%q) = true", s)
		}
	}
}

// TestDataDiskNames: the temporary name extends the final one, so a
// listing shows them together, and neither has the .img or .dmg of an
// image the host would attach (S06-05-disks, "Names on the host").
func TestDataDiskNames(t *testing.T) {
	if !strings.HasPrefix(vmbundle.DataDiskTempName, vmbundle.DataDiskName+".") {
		t.Errorf("temporary name %s doesn't extend %s", vmbundle.DataDiskTempName, vmbundle.DataDiskName)
	}
	for _, name := range []string{vmbundle.DataDiskName, vmbundle.DataDiskTempName} {
		for _, ext := range []string{".img", ".dmg", ".iso", ".sparseimage"} {
			if strings.HasSuffix(name, ext) {
				t.Errorf("%s ends in %s", name, ext)
			}
		}
	}
}
