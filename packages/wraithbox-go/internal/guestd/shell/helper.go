package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// HelperArg is the one argument that makes wb-guestd's executable run
// as a session helper (S06-09-session-lifecycle, "Session helper").
// cmd/wb-guestd checks it before any other start-up code.
const HelperArg = "session-helper"

// helperConfig is what the helper runs. Production runs /bin/zsh -l.
// Tests run a test program through their own entry point, which the
// production build doesn't have.
type helperConfig struct {
	path string
	args []string
	// tempDir reads the user's temporary directory, "" for none.
	tempDir func(context.Context) (string, error)
}

// tempDirWait bounds the read of the user's temporary directory.
const tempDirWait = 5 * time.Second

// HelperMain runs the session helper, and returns its exit code: 0
// after it reported its program's end, 1 when it didn't start it.
func HelperMain() int {
	return runHelper(helperConfig{path: shellPath, args: []string{"zsh", "-l"}, tempDir: sessionproc.UserTempDir})
}

// runHelper is the session helper. wb-guestd started it as the project
// user, as a new POSIX session's leader, with the PTY's replica as its
// controlling terminal in interactive use, the program's standard
// streams on 0 to 2, the report pipe on 3, and the command pipe on 4.
func runHelper(cfg helperConfig) int {
	pid, err := startProgram(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wb session helper: %v\n", err)
		return 1
	}
	h := &helper{pid: pid, reports: os.NewFile(reportFD, "reports")}
	if err := h.report(Report{Kind: ReportStarted, Value: u32(pid)}); err != nil {
		fmt.Fprintf(os.Stderr, "wb session helper: %v\n", err)
	}
	go h.commands(os.NewFile(commandFD, "commands"))
	h.wait()
	return 0
}

// startProgram checks the helper's start, sets up the session, and
// starts the program.
func startProgram(cfg helperConfig) (int, error) {
	// SEC11-root-gains-nothing: the helper runs only as a project user.
	if sessionproc.IsRoot() {
		return 0, errors.New("refusing to run as root")
	}
	for _, fd := range []int{reportFD, commandFD} {
		if err := sessionproc.CheckPipe(fd); err != nil {
			return 0, err
		}
		sessionproc.CloseOnExec(fd)
	}
	// The kernel sends the session leader SIGHUP when the terminal hangs
	// up. The helper outlives it, to report the program's end. A handler
	// instead of SIG_IGN, because an ignored signal stays ignored in the
	// program.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP)
	// S13-01-process-hardening, "Limits and environment": no core files.
	if err := sessionproc.NoCore(); err != nil {
		return 0, err
	}
	// The helper changes to the home as the user, so root never opens a
	// path under it (S06-09-session-lifecycle, "As the project user").
	if err := os.Chdir(os.Getenv("HOME")); err != nil {
		return 0, fmt.Errorf("change to the home directory: %w", err)
	}
	env, err := withTempDir(cfg.tempDir)
	if err != nil {
		return 0, err
	}
	_, ttyErr := sessionproc.ForegroundGroup(os.Stdin)
	return sessionproc.StartProgram(sessionproc.ProgramSpec{
		Path: cfg.path, Args: cfg.args, Env: env, Foreground: ttyErr == nil,
	})
}

// withTempDir returns the helper's environment, which wb-guestd built,
// with the user's TMPDIR when there is one. On macOS it is the user's
// own directory under /var/folders, so temporary files stay out of the
// shared /tmp.
func withTempDir(read func(context.Context) (string, error)) ([]string, error) {
	env := os.Environ()
	ctx, cancel := context.WithTimeout(context.Background(), tempDirWait)
	defer cancel()
	dir, err := read(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the temporary directory: %w", err)
	}
	if dir != "" {
		env = append(env, "TMPDIR="+dir)
	}
	return env, nil
}

// helper reports the program's events, and applies commands to its
// process group until the program has ended.
type helper struct {
	pid     int
	reports *os.File

	mu    sync.Mutex
	ended bool
}

func (h *helper) report(r Report) error {
	rec := r.Encode()
	_, err := h.reports.Write(rec[:])
	return err
}

// wait reports each stop, continue and the end of the program, and
// returns after the end.
func (h *helper) wait() {
	for {
		st, err := sessionproc.Wait(h.pid)
		if err != nil {
			// No report: wb-guestd reads the end of the reports as
			// session-helper-gone.
			fmt.Fprintf(os.Stderr, "wb session helper: %v\n", err)
			return
		}
		r, final := statusReport(st)
		if final {
			h.mu.Lock()
			h.ended = true
			h.mu.Unlock()
		}
		_ = h.report(r)
		if final {
			_ = h.reports.Close()
			return
		}
	}
}

func statusReport(st sessionproc.Status) (Report, bool) {
	switch st.Event {
	case sessionproc.Stopped:
		return Report{Kind: ReportStopped}, false
	case sessionproc.Continued:
		return Report{Kind: ReportContinued}, false
	case sessionproc.Exited:
		return Report{Kind: ReportExited, Value: u32(st.Code)}, true
	case sessionproc.Signaled:
		return Report{Kind: ReportSignaled, Value: u32(st.Signal)}, true
	}
	// Wait returns no other event. A report of kind 0, which wb-guestd
	// refuses, ends the session.
	return Report{}, true
}

// commands reads command records, and applies each to the program's
// process group. The end of the commands, or a record out of form,
// counts as a hangup, after which it reads no more.
func (h *helper) commands(r io.Reader) {
	for {
		var b [RecordSize]byte
		_, err := io.ReadFull(r, b[:])
		c, sig, ok := parseCommand(b)
		if err != nil || !ok {
			h.apply(CommandHangup, 0)
			return
		}
		h.apply(c, sig)
	}
}

// apply sends a command's signals to the program's process group, while
// the program hasn't been reaped, so a group ID that is reused later
// gets none.
func (h *helper) apply(c Command, sig sessionproc.Signal) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return
	}
	var sigs []sessionproc.Signal
	switch c {
	case CommandHangup:
		sigs = []sessionproc.Signal{sessionproc.SigHup, sessionproc.SigCont}
	case CommandStop:
		sigs = []sessionproc.Signal{sessionproc.SigStop}
	case CommandResume:
		sigs = []sessionproc.Signal{sessionproc.SigCont}
	case CommandSignal:
		sigs = []sessionproc.Signal{sig}
	}
	for _, s := range sigs {
		if err := sessionproc.SignalGroup(h.pid, s); err != nil {
			fmt.Fprintf(os.Stderr, "wb session helper: %v\n", err)
		}
	}
}
