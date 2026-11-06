//go:build darwin || linux

package fileowner

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestUID: a file this process made is owned by its effective user ID,
// and info that stat(2) didn't make is an error.
func TestUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UID(info)
	if err != nil || int(got) != os.Geteuid() {
		t.Fatalf("UID = %d, %v; want %d", got, err, os.Geteuid())
	}
	mem, err := fstest.MapFS{"f": &fstest.MapFile{}}.Stat("f")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UID(mem); err == nil {
		t.Fatal("UID of an in-memory file: no error")
	}
}
