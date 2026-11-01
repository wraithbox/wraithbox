//go:build darwin || linux

package volspace

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func stat(path string) (Space, error) {
	var fs unix.Statfs_t
	if err := unix.Statfs(path, &fs); err != nil {
		return Space{}, fmt.Errorf("volspace: statfs %s: %w", path, err)
	}
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return Space{}, fmt.Errorf("volspace: stat %s: %w", path, err)
	}
	vol, err := volumeOf(&st)
	if err != nil {
		return Space{}, fmt.Errorf("volspace: stat %s: %w", path, err)
	}
	return Space{Free: freeBytes(&fs), Volume: vol}, nil
}

// blockSize is the unit of st_blocks on macOS and Linux.
const blockSize = 512

func allocated(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return 0, fmt.Errorf("volspace: lstat %s: %w", path, err)
	}
	if st.Blocks < 0 {
		return 0, fmt.Errorf("volspace: lstat %s: %d blocks", path, st.Blocks)
	}
	return uint64(st.Blocks) * blockSize, nil
}

func allocate(path string, size int64) error {
	if size <= 0 {
		return fmt.Errorf("volspace: allocate %s: size %d isn't positive", path, size)
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("volspace: create %s: %w", path, err)
	}
	err = preallocate(fd, size)
	if err == nil {
		err = unix.Fsync(fd)
	}
	if cerr := unix.Close(fd); err == nil {
		err = cerr
	}
	if err == nil {
		return nil
	}
	err = fmt.Errorf("volspace: allocate %d bytes to %s: %w", size, path, err)
	if rerr := os.Remove(path); rerr != nil {
		err = errors.Join(err, fmt.Errorf("volspace: remove the partial file: %w", rerr))
	}
	return err
}
