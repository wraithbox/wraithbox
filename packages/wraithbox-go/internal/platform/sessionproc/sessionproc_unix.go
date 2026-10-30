//go:build darwin || linux

package sessionproc

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// ReplicaMode is the mode of a session's PTY replica: its user reads
// and writes it, and nobody else (S13-01-process-hardening, "Limits and
// environment").
const ReplicaMode = 0o600

// OpenPTY opens a PTY pair, gives the replica to owner with mode
// ReplicaMode, and sets the window size. The controller is
// non-blocking and close-on-exec, so a Close ends a Read that waits on
// it.
func OpenPTY(owner Cred, rows, cols uint16) (ctl, replica *os.File, err error) {
	p, t, err := pty.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("sessionproc: open a PTY: %w", err)
	}
	// pty.Open's controller is in blocking mode. A copy of it in
	// non-blocking mode goes through Go's poller, so Close interrupts a
	// Read. F_DUPFD_CLOEXEC makes the copy close-on-exec at once, so a
	// helper started meanwhile on another goroutine doesn't get it.
	fd, err := unix.FcntlInt(p.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	_ = p.Close()
	if err != nil {
		_ = t.Close()
		return nil, nil, fmt.Errorf("sessionproc: copy the PTY controller: %w", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		_ = t.Close()
		return nil, nil, fmt.Errorf("sessionproc: make the PTY controller non-blocking: %w", err)
	}
	ctl = os.NewFile(uintptr(fd), "pty-controller")
	fail := func(step string, err error) (*os.File, *os.File, error) {
		_ = ctl.Close()
		_ = t.Close()
		return nil, nil, fmt.Errorf("sessionproc: %s: %w", step, err)
	}
	if err := t.Chown(int(owner.UID), int(owner.GID)); err != nil {
		return fail("give the PTY replica to its user", err)
	}
	if err := t.Chmod(ReplicaMode); err != nil {
		return fail("set the PTY replica's mode", err)
	}
	if err := SetSize(ctl, rows, cols); err != nil {
		return fail("set the window size", err)
	}
	return ctl, t, nil
}

// SetSize sets the window size of the PTY with TIOCSWINSZ, which sends
// SIGWINCH to its foreground process group.
func SetSize(ctl *os.File, rows, cols uint16) error {
	return control(ctl, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
}

// Size reads the window size of a terminal, for tests.
func Size(f *os.File) (rows, cols uint16, err error) {
	err = control(f, func(fd int) error {
		ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		if err == nil {
			rows, cols = ws.Row, ws.Col
		}
		return err
	})
	return rows, cols, err
}

// ForegroundGroup reads the foreground process group of the terminal
// (tcgetpgrp), for tests.
func ForegroundGroup(f *os.File) (int, error) {
	pgid := 0
	err := control(f, func(fd int) error {
		var err error
		pgid, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		return err
	})
	return pgid, err
}

// control runs fn on f's descriptor without putting f in blocking
// mode, as f.Fd() would.
func control(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err := rc.Control(func(fd uintptr) { inner = fn(int(fd)) }); err != nil {
		return err
	}
	return inner
}

// StartHelper starts the session helper as a new POSIX session's
// leader (setsid), with the PTY's replica as its controlling terminal
// when spec.Ctty is set, as spec.Cred with exactly its groups.
func StartHelper(spec HelperSpec) (*os.Process, error) {
	attr := &syscall.SysProcAttr{Setsid: true, Setctty: spec.Ctty, Ctty: 0}
	if c := spec.Cred; c != nil {
		attr.Credential = &syscall.Credential{Uid: c.UID, Gid: c.GID, Groups: c.Groups}
	}
	p, err := os.StartProcess(spec.Path, spec.Args, &os.ProcAttr{
		Dir: "/", Env: spec.Env, Files: spec.Files, Sys: attr,
	})
	if err != nil {
		return nil, fmt.Errorf("sessionproc: start the session helper: %w", err)
	}
	return p, nil
}

// StartProgram starts the helper's program in a process group of its
// own, which leads the terminal's foreground with spec.Foreground. The
// caller reaps it with Wait, never with os.Process.Wait.
func StartProgram(spec ProgramSpec) (int, error) {
	attr := &syscall.SysProcAttr{Setpgid: !spec.Foreground, Foreground: spec.Foreground, Ctty: 0}
	pid, err := syscall.ForkExec(spec.Path, spec.Args, &syscall.ProcAttr{
		Env: spec.Env, Files: []uintptr{0, 1, 2}, Sys: attr,
	})
	if err != nil {
		return 0, fmt.Errorf("sessionproc: start %s: %w", spec.Path, err)
	}
	return pid, nil
}

// Wait waits for the program pid to stop, continue or end, with
// WUNTRACED and WCONTINUED, and reaps it when it ended.
func Wait(pid int) (Status, error) {
	for {
		var ws unix.WaitStatus
		_, err := unix.Wait4(pid, &ws, unix.WUNTRACED|unix.WCONTINUED, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return Status{}, fmt.Errorf("sessionproc: wait for %d: %w", pid, err)
		}
		if st, ok := decode(uint32(ws)); ok {
			return st, nil
		}
	}
}

// SignalGroup sends sig to the process group pgid.
func SignalGroup(pgid int, sig Signal) error {
	if pgid < 2 {
		return ErrPID
	}
	num, ok := unixSignal(sig)
	if !ok {
		return fmt.Errorf("sessionproc: unknown signal %d", int(sig))
	}
	if err := unix.Kill(-pgid, num); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("sessionproc: kill -%d %s: %w", pgid, sig, err)
	}
	return nil
}

func unixSignal(sig Signal) (unix.Signal, bool) {
	switch sig {
	case SigHup:
		return unix.SIGHUP, true
	case SigInt:
		return unix.SIGINT, true
	case SigQuit:
		return unix.SIGQUIT, true
	case SigTerm:
		return unix.SIGTERM, true
	case SigTstp:
		return unix.SIGTSTP, true
	case SigCont:
		return unix.SIGCONT, true
	case SigStop:
		return unix.SIGSTOP, true
	}
	return 0, false
}

// NoCore sets RLIMIT_CORE to 0, so no process of the session writes a
// core file (S13-01-process-hardening, "Limits and environment").
func NoCore() error {
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
		return fmt.Errorf("sessionproc: set RLIMIT_CORE to 0: %w", err)
	}
	return nil
}

// CheckPipe reports an error unless fd is an open pipe.
func CheckPipe(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("sessionproc: descriptor %d: %w", fd, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFIFO {
		return fmt.Errorf("sessionproc: descriptor %d isn't a pipe", fd)
	}
	return nil
}

// CloseOnExec marks fd close-on-exec, so the program doesn't get it.
func CloseOnExec(fd int) { unix.CloseOnExec(fd) }

// IsRoot reports whether the process runs with user ID 0, real or
// effective.
func IsRoot() bool { return unix.Getuid() == 0 || unix.Geteuid() == 0 }

// Getsid returns the POSIX session ID of pid, 0 for the caller.
func Getsid(pid int) (int, error) { return unix.Getsid(pid) }

// Getpgid returns the process group of pid, 0 for the caller.
func Getpgid(pid int) (int, error) { return unix.Getpgid(pid) }
