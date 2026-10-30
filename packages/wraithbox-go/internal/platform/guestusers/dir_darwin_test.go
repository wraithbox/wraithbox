package guestusers_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// TestRootRmdir: Rmdir removes an empty directory, and neither a full
// one, a file nor a link.
func TestRootRmdir(t *testing.T) {
	dir, root := openRoot(t)
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if err := root.Mkdir("empty", uid, gid); err != nil {
		t.Fatal(err)
	}
	if err := root.Rmdir("empty"); err != nil {
		t.Errorf("Rmdir of an empty directory: %v", err)
	}
	if err := root.Mkdir("full", uid, gid); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full", "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"full", "file", "link", "missing", "..", "a/b"} {
		if err := root.Rmdir(name); err == nil {
			t.Errorf("Rmdir(%q) succeeded", name)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "link")); err != nil {
		t.Errorf("the link is gone: %v", err)
	}
}

func openDir(t *testing.T) (string, guestusers.Dir) {
	t.Helper()
	dir, root := openRoot(t)
	if err := root.Mkdir("records", uint32(os.Getuid()), uint32(os.Getgid())); err != nil {
		t.Fatal(err)
	}
	d, err := root.OpenDir("records")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return filepath.Join(dir, "records"), d
}

// TestDirWriteRead: WriteFile replaces a file whole, mode 600, and
// ReadFile, Names and Remove see it.
func TestDirWriteRead(t *testing.T) {
	_, d := openDir(t)
	self, err := d.Self()
	if err != nil || !self.IsDir || self.Perm != 0o700 || self.UID != uint32(os.Getuid()) {
		t.Fatalf("Self = %+v, %v", self, err)
	}
	for _, data := range []string{"first\n", "second\n"} {
		if err := d.WriteFile("10000", []byte(data)); err != nil {
			t.Fatal(err)
		}
		got, e, err := d.ReadFile("10000", 64)
		if err != nil || string(got) != data || e.Perm != 0o600 || e.IsDir {
			t.Errorf("ReadFile = %q, %+v, %v", got, e, err)
		}
	}
}

// TestDirNames: WriteFile replaces a temporary file a crash left, and
// Names sees the files.
func TestDirNames(t *testing.T) {
	path, d := openDir(t)
	if err := d.WriteFile("10000", []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "10001.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFile("10001", []byte("third\n")); err != nil {
		t.Fatal(err)
	}
	names, err := d.Names(10)
	slices.Sort(names)
	if err != nil || !slices.Equal(names, []string{"10000", "10001"}) {
		t.Errorf("Names = %v, %v", names, err)
	}
	if _, err := d.Names(1); !errors.Is(err, guestusers.ErrTooManyEntries) {
		t.Errorf("Names over the limit: %v", err)
	}
}

// TestDirReadRefuses: ReadFile refuses a link, a FIFO, a directory and
// a file over the limit, and never blocks on the FIFO.
func TestDirReadRefuses(t *testing.T) {
	path, d := openDir(t)
	if err := os.Symlink("/etc/hosts", filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(path, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "big"), []byte(strings.Repeat("x", 65)), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]error{
		"link": syscall.ELOOP, "fifo": guestusers.ErrNotRegular,
		"dir": guestusers.ErrNotRegular, "big": guestusers.ErrTooBig,
		"missing": fs.ErrNotExist,
	} {
		if _, _, err := d.ReadFile(name, 64); !errors.Is(err, want) {
			t.Errorf("ReadFile(%q): %v, want %v", name, err, want)
		}
	}
	// A link at the name of a write is replaced, not followed.
	if err := d.WriteFile("link", []byte("y")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(path, "link")); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("after WriteFile the link is %v, %v", fi, err)
	}
}

// TestRootOpenDirLink: OpenDir refuses a link.
func TestRootOpenDirLink(t *testing.T) {
	dir, root := openRoot(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.OpenDir("link"); err == nil {
		t.Error("OpenDir followed a link")
	}
}

// TestEmptyHomeRefuses: EmptyHome refuses a path that isn't absolute,
// and as a user other than root it can't take on a UID's group list, so
// it fails without removing anything.
func TestEmptyHomeRefuses(t *testing.T) {
	s := &guestusers.System{}
	for _, home := range []string{"", "relative", "/a/../b", "/a\nb"} {
		if err := s.EmptyHome(t.Context(), 10000, 10000, home); err == nil {
			t.Errorf("EmptyHome(%q) succeeded", home)
		}
	}
	if os.Getuid() == 0 {
		t.Skip("runs as root: the conformance checks cover a real run")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.EmptyHome(t.Context(), uint32(os.Getuid()), uint32(os.Getgid()), home); err == nil {
		t.Error("EmptyHome as a user other than root succeeded")
	}
	if _, err := os.Stat(filepath.Join(home, "f")); err != nil {
		t.Errorf("the file is gone: %v", err)
	}
}
