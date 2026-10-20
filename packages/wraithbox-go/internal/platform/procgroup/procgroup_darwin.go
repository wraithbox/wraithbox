package procgroup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

const groups = true

// newGroup makes cmd's child lead a new process group, whose id is its
// pid: setpgid(0, 0) in the child, before exec.
func newGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
}

// watch registers a kqueue EVFILT_PROC watch for pid's exit, and
// returns a function that blocks until it fires. The watch doesn't reap
// pid. A pid that has already exited, and waits as a zombie to be
// reaped, can't be registered: the function then returns at once.
func watch(pid int) (func(), error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	var ev unix.Kevent_t
	unix.SetKevent(&ev, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	ev.Fflags = unix.NOTE_EXIT
	_, err = unix.Kevent(kq, []unix.Kevent_t{ev}, nil, nil)
	switch {
	case errors.Is(err, unix.ESRCH):
		_ = unix.Close(kq)
		return func() {}, nil
	case err != nil:
		_ = unix.Close(kq)
		return nil, err
	}
	return func() { awaitExit(kq) }, nil
}

// awaitExit blocks until the watch on kq fires, then closes kq. Any
// error other than an interrupt ends the wait too: the caller then
// reaps, which blocks until the exit.
func awaitExit(kq int) {
	defer func() { _ = unix.Close(kq) }()
	out := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, nil, out, nil)
		if errors.Is(err, unix.EINTR) || (err == nil && n == 0) {
			continue
		}
		return
	}
}

// kill sends SIGKILL to p's process group, unless p has exited.
func kill(p *os.Process, exited bool) error {
	if exited {
		return os.ErrProcessDone
	}
	return signalGroup(p.Pid, unix.SIGKILL)
}

// signalGroup sends sig to the process group pid leads, and then to pid
// itself, in case it left that group. A group that is gone, because pid
// left it and no member is left, isn't an error. pid must be a started
// child's, above 1: kill(2) reads -0 as the caller's own group, and -1
// as every process the caller may signal. Any other pid is refused, and
// nothing is sent.
func signalGroup(pid int, sig unix.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("procgroup: %d is no child's process group, so nothing is signaled", pid)
	}
	groupErr := unix.Kill(-pid, sig)
	if errors.Is(groupErr, unix.ESRCH) {
		groupErr = nil
	}
	return errors.Join(groupErr, unix.Kill(pid, sig))
}
