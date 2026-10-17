package diskimage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
)

// Files is the file system of internal/diskfmt on macOS. Every call
// acts on the name it gets and never follows a symbolic link there.
type Files struct{}

// NewFiles returns the file system.
func NewFiles() (Files, error) { return Files{}, nil }

var _ diskfmt.Files = Files{}

// Create creates path with O_CREAT|O_EXCL|O_NOFOLLOW and mode 0600, and
// sets its size with ftruncate, which allocates no block, as mkfile
// does (S06-13-data-disk-format, "The shim", step 1).
func (Files) Create(path string, size int64) (diskfmt.Handle, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	if err := unix.Ftruncate(fd, size); err != nil {
		return nil, errors.Join(&fs.PathError{Op: "ftruncate", Path: path, Err: err}, f.Close())
	}
	return handle{f: f}, nil
}

// Lstat returns the identity of the name path.
func (Files) Lstat(path string) (diskfmt.Identity, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return diskfmt.Identity{}, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	return identity(&st), nil
}

// Unlink removes the name path. It fails on a directory.
func (Files) Unlink(path string) error {
	if err := unix.Unlink(path); err != nil {
		return &fs.PathError{Op: "unlink", Path: path, Err: err}
	}
	return nil
}

// RenameExcl renames with renamex_np and RENAME_EXCL, which fails with
// EEXIST when to has a file (S06-13-data-disk-format, "The shim", step 8).
func (Files) RenameExcl(from, to string) error {
	if err := unix.RenamexNp(from, to, unix.RENAME_EXCL); err != nil {
		return &os.LinkError{Op: "renamex_np", Old: from, New: to, Err: err}
	}
	return nil
}

type handle struct{ f *os.File }

// Stat is fstat on the kept descriptor.
func (h handle) Stat() (diskfmt.Identity, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(h.f.Fd()), &st); err != nil {
		return diskfmt.Identity{}, &fs.PathError{Op: "fstat", Path: h.f.Name(), Err: err}
	}
	return identity(&st), nil
}

func (h handle) Close() error { return h.f.Close() }

func identity(st *unix.Stat_t) diskfmt.Identity {
	return diskfmt.Identity{
		Dev: int64(st.Dev), Ino: st.Ino, Nlink: uint64(st.Nlink), Blocks: st.Blocks,
		Regular: st.Mode&unix.S_IFMT == unix.S_IFREG,
	}
}

// Allocated returns the bytes the file at the name path takes on the
// host, from its st_blocks, read with lstat: never from an image tool,
// and without opening the file (S06-05-disks, "Disks";
// SEC15-no-guest-disk-mount). A name that isn't a regular file is an
// error.
func Allocated(path string) (int64, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return 0, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, fmt.Errorf("diskimage: %s isn't a regular file", path)
	}
	// st_blocks counts 512-byte blocks (sys/stat.h).
	return st.Blocks * 512, nil
}
