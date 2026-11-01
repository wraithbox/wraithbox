//go:build darwin || linux

package volspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestStat reads the free bytes and the volume of a temporary
// directory. A file in it is on the same volume.
func TestStat(t *testing.T) {
	dir := t.TempDir()
	sp, err := Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Free == 0 {
		t.Errorf("Stat(%s).Free = 0", dir)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileSp, err := Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fileSp.Volume != sp.Volume {
		t.Errorf("a file in %s is on volume %d, the directory on %d", dir, fileSp.Volume, sp.Volume)
	}
	if _, err := Stat(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of a missing path: %v, want fs.ErrNotExist", err)
	}
}

// TestAllocatedSparse sees a sparse file of 64 MiB take less than its
// size.
func TestAllocatedSparse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := Allocated(path)
	if err != nil {
		t.Fatal(err)
	}
	if n >= 64<<20 {
		t.Errorf("Allocated(sparse) = %d, want under %d", n, 64<<20)
	}
}

// TestAllocate allocates 64 MiB, and sees at least 64 MiB allocated and
// the size set. A second Allocate at the same name fails, and a link at
// the name isn't followed.
func TestAllocate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reserve")
	const size = 64 << 20
	if err := Allocate(path, size); err != nil {
		t.Fatal(err)
	}
	n, err := Allocated(path)
	if err != nil {
		t.Fatal(err)
	}
	if n < size {
		t.Errorf("Allocated = %d, want at least %d", n, size)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != size || info.Mode().Perm() != 0o600 {
		t.Errorf("reserve is %d bytes, mode %v, want %d bytes, 0600", info.Size(), info.Mode().Perm(), size)
	}
	if err := Allocate(path, size); !errors.Is(err, fs.ErrExist) {
		t.Errorf("a second Allocate: %v, want fs.ErrExist", err)
	}
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Allocate(link, size); err == nil {
		t.Error("Allocate followed a symbolic link")
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Allocate through a link made its target: %v", err)
	}
	if err := Allocate(filepath.Join(dir, "zero"), 0); err == nil {
		t.Error("Allocate of 0 bytes succeeded")
	}
}

// TestAllocatedLink doesn't follow a symbolic link.
func TestAllocatedLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "big")
	if err := Allocate(target, 8<<20); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	n, err := Allocated(link)
	if err != nil {
		t.Fatal(err)
	}
	if n >= 8<<20 {
		t.Errorf("Allocated(link) = %d: it followed the link", n)
	}
}
