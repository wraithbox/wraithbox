package diskclone

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Clone creates dst as an APFS clone of src. src must be a regular
// file, checked with lstat, and the file opened must be the one checked.
// dst's directory is opened once, without following a symbolic link, and
// the clone is created relative to it with CLONE_NOFOLLOW: it fails with
// EEXIST when anything is at dst, a symbolic link included, and never
// writes over or opens it (SEC15-no-guest-disk-mount). src and dst must
// be on the same APFS volume. There is no copy to fall back to.
func (Cloner) Clone(src, dst string) error {
	in, err := openSource(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	dir, err := unix.Open(filepath.Dir(dst), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("diskclone: open the directory of %s: %w", dst, err)
	}
	defer func() { _ = unix.Close(dir) }()
	if err := unix.Fclonefileat(int(in.Fd()), dir, filepath.Base(dst), unix.CLONE_NOFOLLOW); err != nil {
		return fmt.Errorf("diskclone: clone %s to %s: %w", src, dst, err)
	}
	return nil
}

// openSource opens src, which must be a regular file and not a symbolic
// link: an lstat checks it, O_NOFOLLOW refuses a link made since, and
// the opened file must be the one the lstat saw.
func openSource(src string) (*os.File, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return nil, fmt.Errorf("diskclone: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("diskclone: the source %s is not a regular file (%s)", src, info.Mode().Type())
	}
	in, err := os.OpenFile(src, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("diskclone: %w", err)
	}
	opened, err := in.Stat()
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("it changed since it was checked")
	}
	if err != nil {
		_ = in.Close()
		return nil, fmt.Errorf("diskclone: the source %s: %w", src, err)
	}
	return in, nil
}
