package volspace

import "golang.org/x/sys/unix"

// volumeOf is the device of a file's volume.
func volumeOf(st *unix.Stat_t) (uint64, error) { return st.Dev, nil }

// freeBytes is the space statfs reports free to a process without
// privileges. Linux gives the block size as a signed number, which is
// never negative.
func freeBytes(fs *unix.Statfs_t) uint64 {
	if fs.Bsize <= 0 {
		return 0
	}
	return fs.Bavail * uint64(fs.Bsize)
}

// preallocate allocates size bytes from the start of fd with
// fallocate(2), mode 0, which also sets the file's length.
func preallocate(fd int, size int64) error {
	return unix.Fallocate(fd, 0, 0, size)
}
