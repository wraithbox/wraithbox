package fdchan

import (
	"fmt"
	"syscall"
)

// maxFDScan bounds the walks over the descriptor table. A descriptor
// limit above it is refused, not walked in part.
const maxFDScan = 1 << 20

// fdLimit returns the descriptor limit (RLIMIT_NOFILE) a walk goes up
// to, or an error when it is over maxFDScan.
func fdLimit() (int, error) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, fmt.Errorf("fdchan: descriptor limit: %w", err)
	}
	if lim.Cur > maxFDScan {
		return 0, fmt.Errorf("fdchan: descriptor limit %d is over %d", lim.Cur, maxFDScan)
	}
	return int(lim.Cur), nil
}

// fdFlags returns the descriptor flags of fd, and false when fd isn't
// open.
func fdFlags(fd int) (uintptr, bool) {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	return flags, errno == 0
}

// FirstOpenFrom returns the lowest open descriptor of the process that is
// at least from, or -1 when none is open below the descriptor limit
// (RLIMIT_NOFILE). A process whose start interface is a fixed set of
// descriptors uses it to refuse one it didn't expect.
func FirstOpenFrom(from int) (int, error) {
	limit, err := fdLimit()
	if err != nil {
		return 0, err
	}
	for fd := max(from, 0); fd < limit; fd++ {
		if _, open := fdFlags(fd); open {
			return fd, nil
		}
	}
	return -1, nil
}

// CloseOnExecFrom marks every open descriptor of the process from from
// on close-on-exec, and returns how many it marked. A descriptor the
// process inherited without the flag, from launchd or a shell, then
// doesn't reach a child it starts. os/exec still places the files a
// command names in the child.
func CloseOnExecFrom(from int) (int, error) {
	limit, err := fdLimit()
	if err != nil {
		return 0, err
	}
	marked := 0
	for fd := max(from, 0); fd < limit; fd++ {
		flags, open := fdFlags(fd)
		if !open || flags&syscall.FD_CLOEXEC != 0 {
			continue
		}
		syscall.CloseOnExec(fd)
		marked++
	}
	return marked, nil
}
