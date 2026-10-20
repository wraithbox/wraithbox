package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanonicalDir: links are resolved and each name gets its case on
// disk, so every spelling of one directory gives one string
// (S05-04-naming, "Path").
func TestCanonicalDir(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "git", "Foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	want, err := CanonicalDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(want, "/git/Foo") {
		t.Errorf("CanonicalDir(%s) = %s", dir, want)
	}
	spellings := []string{link, filepath.Join(dir, "..", "Foo"), filepath.Join(link, ".")}
	if _, err := os.Stat(filepath.Join(tmp, "GIT", "foo")); err == nil {
		spellings = append(spellings, filepath.Join(tmp, "GIT", "foo"))
	}
	for _, p := range spellings {
		if got, err := CanonicalDir(p); err != nil || got != want {
			t.Errorf("CanonicalDir(%s) = %q, %v; want %q", p, got, err, want)
		}
	}
}

func TestCanonicalDirRefuses(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalDir(file); err == nil {
		t.Error("CanonicalDir(a file): want an error")
	}
	if _, err := CanonicalDir(filepath.Join(tmp, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CanonicalDir(missing) = %v, want fs.ErrNotExist", err)
	}
}
