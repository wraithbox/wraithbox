package fdchan

import (
	"fmt"
	"syscall"
)

// maxFDScan bounds FirstOpenFrom's walk. A descriptor limit above it is
// refused, not walked in part.
const maxFDScan = 1 << 20

// FirstOpenFrom returns the lowest open descriptor of the process that is
// at least from, or -1 when none is open below the descriptor limit
// (RLIMIT_NOFILE). A process whose start interface is a fixed set of
// descriptors uses it to refuse one it didn't expect.
func FirstOpenFrom(from int) (int, error) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, fmt.Errorf("fdchan: descriptor limit: %w", err)
	}
	if lim.Cur > maxFDScan {
		return 0, fmt.Errorf("fdchan: descriptor limit %d is over %d", lim.Cur, maxFDScan)
	}
	for fd := max(from, 0); fd < int(lim.Cur); fd++ {
		if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0); errno == 0 {
			return fd, nil
		}
	}
	return -1, nil
}
