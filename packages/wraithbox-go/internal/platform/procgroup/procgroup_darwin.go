package procgroup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

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
	return func() { awaitExit(kq, pid) }, nil
}

// awaitExit blocks until the watch on kq fires, then closes kq. A
// kevent error other than an interrupt is logged, and the wait goes on
// by polling until pid is a zombie, so the gate on Kill stays closed
// while pid runs.
func awaitExit(kq, pid int) {
	defer func() { _ = unix.Close(kq) }()
	out := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, nil, out, nil)
		switch {
		case err == nil && n > 0:
			return
		case err == nil || errors.Is(err, unix.EINTR):
			continue
		}
		slog.Default().LogAttrs(context.Background(), slog.LevelWarn, "procgroup: the kqueue exit watch failed, polling for the exit",
			slog.Int("pid", pid), slog.String("error", err.Error()))
		for !zombie(pid) {
			time.Sleep(pollInterval)
		}
		return
	}
}

// pollInterval is how often awaitExit polls after the kqueue failed.
const pollInterval = 100 * time.Millisecond

// zombie reports whether pid has exited and waits to be reaped. Its
// parent, this process, hasn't reaped it, so the pid isn't reused. An
// error, as when the sysctl fails, reports false: the caller polls on,
// with the gate closed.
func zombie(pid int) bool {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && k.Proc.P_stat == stateZombie
}

// stateZombie is the p_stat value in <sys/proc.h> of a process that has
// exited and isn't reaped yet.
const stateZombie = 5

// kill sends SIGKILL to p's process group, unless p has exited.
func kill(p *os.Process, exited bool) error {
	if exited {
		return os.ErrProcessDone
	}
	return signalGroup(p.Pid, unix.SIGKILL)
}

// signalGroup sends sig to the process group pid leads, and then to pid
// itself, in case it left that group. pid must be a started child's,
// above 1: kill(2) reads -0 as the caller's own group, and -1 as every
// process the caller may signal. Any other pid is refused, and nothing
// is sent.
//
// A group with no live member isn't an error when the signal to pid is
// sent. kill(2) answers ESRCH for a group that is gone, and on macOS
// EPERM for one whose only member is pid as a zombie. A zombie accepts
// a signal, and a sandbox's denial doesn't, so EPERM with a refused
// signal to pid is returned.
func signalGroup(pid int, sig unix.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("procgroup: %d is no child's process group, so nothing is signaled", pid)
	}
	groupErr := unix.Kill(-pid, sig)
	pidErr := unix.Kill(pid, sig)
	if errors.Is(groupErr, unix.ESRCH) || (pidErr == nil && errors.Is(groupErr, unix.EPERM)) {
		groupErr = nil
	}
	return errors.Join(groupErr, pidErr)
}
