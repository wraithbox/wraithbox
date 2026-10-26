package guestusers_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

func openRoot(t *testing.T) (string, guestusers.Root) {
	t.Helper()
	dir := t.TempDir()
	root, err := guestusers.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return dir, root
}

// TestRootMkdir: a new home is a directory with mode 700 and the owner
// given, whatever the umask, and a second Mkdir of the name fails.
func TestRootMkdir(t *testing.T) {
	dir, root := openRoot(t)
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if err := root.Mkdir("wbp10000", uid, gid); err != nil {
		t.Fatal(err)
	}
	e, err := root.Stat("wbp10000")
	if err != nil {
		t.Fatal(err)
	}
	if !e.IsDir || e.IsLink || e.Perm != 0o700 || e.Special || e.UID != uid || e.GID != gid {
		t.Errorf("new home %+v", e)
	}
	if err := root.Mkdir("wbp10000", uid, gid); !errors.Is(err, fs.ErrExist) {
		t.Errorf("second Mkdir: %v, want fs.ErrExist", err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "wbp10000")); err != nil || !fi.IsDir() {
		t.Errorf("lstat: %v, %v", fi, err)
	}
}

// TestRootLink: a link at a name is reported as a link and never
// followed, Mkdir refuses it, and a link as the root itself is refused.
func TestRootLink(t *testing.T) {
	dir, root := openRoot(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "wbp10000")); err != nil {
		t.Fatal(err)
	}
	e, err := root.Stat("wbp10000")
	if err != nil {
		t.Fatal(err)
	}
	if !e.IsLink || e.IsDir {
		t.Errorf("link reported as %+v", e)
	}
	if err := root.Mkdir("wbp10000", uint32(os.Getuid()), uint32(os.Getgid())); err == nil {
		t.Error("Mkdir over a link worked")
	}
	if _, err := guestusers.OpenRoot(filepath.Join(dir, "wbp10000")); err == nil {
		t.Error("OpenRoot followed a link")
	}
}

// TestRootNames: Stat and Mkdir refuse anything but one path component.
func TestRootNames(t *testing.T) {
	_, root := openRoot(t)
	for _, name := range []string{"", ".", "..", "a/b", "../x", "a\x00b"} {
		if _, err := root.Stat(name); err == nil {
			t.Errorf("Stat(%q) worked", name)
		}
		if err := root.Mkdir(name, 0, 0); err == nil {
			t.Errorf("Mkdir(%q) worked", name)
		}
	}
	if _, err := root.Stat("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of a missing entry: %v, want fs.ErrNotExist", err)
	}
}

// TestRootEntries: every entry with its owner, and an error over the
// limit.
func TestRootEntries(t *testing.T) {
	dir, root := openRoot(t)
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := root.Entries(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("%d entries, want 3", len(entries))
	}
	for _, e := range entries {
		if e.UID != uint32(os.Getuid()) || e.IsDir {
			t.Errorf("entry %+v", e)
		}
	}
	if _, err := root.Entries(2); !errors.Is(err, guestusers.ErrTooManyEntries) {
		t.Errorf("Entries(2) of 3: %v, want ErrTooManyEntries", err)
	}
}

// TestRootMkdirUndone: when a step after mkdirat fails, here fchown to
// root without being root, the new directory is removed again, so no
// home owned by root stays behind.
func TestRootMkdirUndone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("fchown to root works as root")
	}
	dir, root := openRoot(t)
	if err := root.Mkdir("wbp10000", 0, 0); err == nil {
		t.Fatal("Mkdir with fchown to root worked")
	}
	if _, err := os.Lstat(filepath.Join(dir, "wbp10000")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("directory after the failed Mkdir: %v, want fs.ErrNotExist", err)
	}
}

// TestRootEntriesLongName: an entry whose name is over 255 bytes, which
// APFS allows as long as it is at most 255 characters, is listed. Stat
// and Mkdir still refuse a name that long.
func TestRootEntriesLongName(t *testing.T) {
	dir, root := openRoot(t)
	long := strings.Repeat("é", 150)
	if err := os.WriteFile(filepath.Join(dir, long), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := root.Entries(10)
	if err != nil || len(entries) != 1 || entries[0].Name != long {
		t.Fatalf("Entries: %v, %v", entries, err)
	}
	if _, err := root.Stat(long); err == nil {
		t.Error("Stat of a 300-byte name worked")
	}
	if err := root.Mkdir(long+"x", 0, 0); err == nil {
		t.Error("Mkdir of a 301-byte name worked")
	}
}
