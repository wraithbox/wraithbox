package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckOwnDir: the user's own directory passes. A missing path, a
// file, and a symbolic link to the user's own directory are each
// refused, and the link is not followed. A directory of another user is
// refused too (/private/var/root is root's).
func TestCheckOwnDir(t *testing.T) {
	dir := t.TempDir()
	if err := CheckOwnDir(dir); err != nil {
		t.Fatalf("own directory: %v", err)
	}
	if err := CheckOwnDir(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v, want fs.ErrNotExist", err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckOwnDir(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckOwnDir(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("link: %v", err)
	}
	if os.Geteuid() != 0 {
		if err := CheckOwnDir("/private/var/root"); err == nil || !strings.Contains(err.Error(), "owned by user ID 0") {
			t.Errorf("root's directory: %v", err)
		}
	}
}
