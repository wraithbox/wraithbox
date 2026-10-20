package platform

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// CanonicalDir returns the canonical form of the directory at path: every
// symbolic link resolved, and each name in the letter case it has on disk
// (S05-04-naming, "Resolving the key"). So `~/Git/foo`, `~/git/foo` and a
// link to it give one string on a case-insensitive APFS volume. path must
// name a directory: anything else is an error, and so is a path that
// doesn't exist.
//
// It opens the directory and asks the kernel for the path of the open
// descriptor with fcntl(F_GETPATH), which gives the names the volume
// stores. filepath.EvalSymlinks keeps the case it was given.
func CanonicalDir(path string) (string, error) {
	// O_DIRECTORY refuses anything but a directory, so a FIFO can't
	// block the open.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("open the directory %q: %w", path, err)
	}
	defer func() { _ = unix.Close(fd) }()

	// F_GETPATH writes up to MAXPATHLEN bytes, NUL-terminated, through
	// the pointer argument. x/sys has no F_GETPATH wrapper, so the
	// buffer goes through FcntlFlock, which passes its pointer on as an
	// unsafe.Pointer, so the buffer stays alive and in place for the
	// call. A uint64 array has the alignment of Flock_t, as the race
	// detector's pointer checks require, and is larger than it.
	var buf [unix.PathMax / 8]uint64
	if err := unix.FcntlFlock(uintptr(fd), unix.F_GETPATH, (*unix.Flock_t)(unsafe.Pointer(&buf))); err != nil {
		return "", fmt.Errorf("get the path of the directory %q: %w", path, err)
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&buf)), len(buf)*8)
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	if n == 0 || n == len(b) {
		return "", fmt.Errorf("get the path of the directory %q: the kernel returned no terminated path", path)
	}
	return string(b[:n]), nil
}
