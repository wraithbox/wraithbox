package diskclone

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// clonePair returns a source with content "tools" and a destination
// name in another directory, both in temporary directories on the same
// volume.
func clonePair(t *testing.T) (src, dst string) {
	t.Helper()
	src = filepath.Join(t.TempDir(), "wb-guest-tools.img")
	write(t, src, "tools")
	return src, filepath.Join(t.TempDir(), "tools.img.partial")
}

// The clone has the source's content, and changing it leaves the source
// alone: it is a copy, not a link.
func TestClone(t *testing.T) {
	src, dst := clonePair(t)
	if err := (Cloner{}).Clone(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dst); got != "tools" {
		t.Fatalf("clone = %q", got)
	}
	write(t, dst, "changed")
	if got := read(t, src); got != "tools" {
		t.Fatalf("the source changed to %q", got)
	}
}

// An existing file at the destination isn't written over
// (SEC15-no-guest-disk-mount: the clone never opens it).
func TestExistingDestinationNotWrittenOver(t *testing.T) {
	src, dst := clonePair(t)
	write(t, dst, "left by a crash")
	err := (Cloner{}).Clone(src, dst)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Clone = %v, want EEXIST", err)
	}
	if got := read(t, dst); got != "left by a crash" {
		t.Fatalf("the destination changed to %q", got)
	}
}

// A symbolic link at the destination is refused and not followed: its
// target, existing or not, is left alone.
func TestDestinationSymlinkNotFollowed(t *testing.T) {
	for _, exists := range []bool{true, false} {
		src, dst := clonePair(t)
		target := filepath.Join(t.TempDir(), "host-file")
		if exists {
			write(t, target, "host data")
		}
		if err := os.Symlink(target, dst); err != nil {
			t.Fatal(err)
		}
		if err := (Cloner{}).Clone(src, dst); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("target exists %v: Clone = %v, want EEXIST", exists, err)
		}
		_, err := os.Lstat(target)
		switch {
		case exists && read(t, target) != "host data":
			t.Errorf("the link's target changed")
		case !exists && !errors.Is(err, fs.ErrNotExist):
			t.Errorf("the link's target was made: %v", err)
		}
	}
}

// A destination directory that is a symbolic link is refused, so the
// clone can't land outside the bundle.
func TestDestinationDirSymlinkRefused(t *testing.T) {
	src, _ := clonePair(t)
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "bundle")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := (Cloner{}).Clone(src, filepath.Join(link, "tools.img.partial")); err == nil {
		t.Fatal("Clone through a linked directory succeeded")
	}
	if m, _ := filepath.Glob(filepath.Join(outside, "*")); len(m) != 0 {
		t.Fatalf("Clone made %v", m)
	}
}

// A source that is a symbolic link, to a file or not, or anything but
// a regular file, is refused, and nothing is made.
func TestSourceNotRegularRefused(t *testing.T) {
	file, dst := clonePair(t)
	link := filepath.Join(t.TempDir(), "wb-guest-tools.img")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"link":      link,
		"directory": t.TempDir(),
		"missing":   filepath.Join(t.TempDir(), "none.img"),
	} {
		if err := (Cloner{}).Clone(src, dst); err == nil {
			t.Errorf("%s: Clone succeeded", name)
		}
		if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: Clone made %s: %v", name, dst, err)
		}
	}
}
