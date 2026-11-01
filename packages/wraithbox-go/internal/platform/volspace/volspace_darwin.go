package volspace

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// volumeOf is the device of a file's volume. macOS gives it as a signed
// number, which is never negative for a file on a mounted volume.
func volumeOf(st *unix.Stat_t) (uint64, error) {
	if st.Dev < 0 {
		return 0, fmt.Errorf("device %d", st.Dev)
	}
	return uint64(st.Dev), nil
}

// freeBytes is the space statfs reports free to a process without
// privileges.
func freeBytes(fs *unix.Statfs_t) uint64 {
	return fs.Bavail * uint64(fs.Bsize)
}

// preallocate allocates size bytes from the start of the empty file fd
// with F_PREALLOCATE, contiguous when the volume can, then sets the
// file's length, which keeps the blocks allocated.
func preallocate(fd int, size int64) error {
	store := unix.Fstore_t{Flags: unix.F_ALLOCATECONTIG | unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: size}
	if err := unix.FcntlFstore(uintptr(fd), unix.F_PREALLOCATE, &store); err != nil {
		store.Flags = unix.F_ALLOCATEALL
		if err := unix.FcntlFstore(uintptr(fd), unix.F_PREALLOCATE, &store); err != nil {
			return err
		}
	}
	return unix.Ftruncate(fd, size)
}
