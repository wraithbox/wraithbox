// Package procgroup starts a program so that a kill stops it and the
// children it started, on macOS so far (S06-13-data-disk-format, "When
// `wb-diskfmt` doesn't exit").
//
// On macOS the program leads a new process group: the child calls
// setpgid before exec. Kill sends SIGKILL to that group and to the
// program. A kqueue watch learns that the program exited without
// reaping it. Kill sends a signal only while the program hasn't
// exited, and the wait that reaps it starts only after Kill can no
// longer send one. A pid, and the group id that equals it, is reused
// only after the reap, so the signal can't reach a process group that
// isn't the program's. A child that leaves the group, with setsid or
// setpgid, isn't stopped.
//
// Linux and Windows don't start a group yet. Kill there is
// os.Process.Kill, which stops the program alone.
package procgroup

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// Groups is whether Kill stops the program's process group on this OS,
// not only the program.
const Groups = groups

// Process is a program started with Start.
type Process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error

	mu sync.Mutex
	// exited: watch reported the exit, and the wait may reap the
	// program, after which its pid can name another process. On Linux
	// and Windows watch reports at once, and kill doesn't read it.
	exited bool
}

// Start starts cmd, in its own process group where the OS has them, and
// waits for it in a goroutine. cmd must not have been started, and the
// caller never calls cmd.Wait: Done and Err report the exit. On macOS a
// program whose exit can't be watched is killed and isn't returned.
func Start(cmd *exec.Cmd) (*Process, error) {
	newGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited, err := watch(cmd.Process.Pid)
	if err != nil {
		_ = kill(cmd.Process, false)
		_ = cmd.Wait()
		return nil, fmt.Errorf("procgroup: watch the exit of %s: %w", cmd.Path, err)
	}
	p := &Process{cmd: cmd, done: make(chan struct{})}
	go p.wait(exited)
	return p, nil
}

// wait blocks until the program exits, closes the gate on Kill, and
// only then reaps it.
func (p *Process) wait(exited func()) {
	exited()
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()
	p.err = p.cmd.Wait()
	close(p.done)
}

// Done is closed once the program has exited and been reaped.
func (p *Process) Done() <-chan struct{} { return p.done }

// Err is cmd.Wait's error, once Done is closed.
func (p *Process) Err() error {
	<-p.done
	return p.err
}

// Pid is the program's process id.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Kill sends SIGKILL to the program's process group, or to the program
// alone where there are no groups. It returns os.ErrProcessDone, and
// sends nothing, once the program has exited on macOS, and once it has
// been reaped on Linux and Windows.
func (p *Process) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return os.ErrProcessDone
	default:
		return kill(p.cmd.Process, p.exited)
	}
}
