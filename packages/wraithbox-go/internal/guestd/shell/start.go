package shell

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// session is one open shell.
type session struct {
	srv   *Server
	id    string
	user  users.User
	setup *terminalv1.Setup
	log   *slog.Logger

	// sid is the helper's PID, which is its POSIX session ID, read from
	// the start, never from a report. 0 until the helper runs. M2 keeps
	// it in memory only.
	sid    atomic.Int32
	helper *os.Process
	// gone is closed once the helper is reaped.
	gone chan struct{}

	// wb-guestd's ends. In interactive use ctl is the PTY's controller,
	// and input and output use it. stderr is nil when standard error is
	// on the PTY.
	ctl      *os.File
	stdin    *os.File
	stdout   *os.File
	stderr   *os.File
	reports  *os.File
	commands *os.File
}

func newSession(srv *Server, id string, u users.User, setup *terminalv1.Setup) *session {
	return &session{
		srv: srv, id: id, user: u, setup: setup, gone: make(chan struct{}),
		log: srv.cfg.Log.With(slog.String("shell_id", id), slog.String("user", u.Name)),
	}
}

// childEnds are the helper's descriptors, which wb-guestd closes once
// the helper has them.
type childEnds []*os.File

// start opens the PTY or the pipes and starts the helper, and returns
// the rule of a failure, or "".
func (s *session) start(ctx context.Context, cfg Config) string {
	// A stop loop that a new connection cancelled may still be ending.
	// Wait for it, so it can't stop what this shell starts.
	cfg.Stopper.Idle()
	cred := s.credential(cfg)
	child, err := s.open(cred)
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "shell start failed",
			slog.String("rule", RuleStartFailed), slog.String("error", err.Error()))
		return RuleStartFailed
	}
	spec := sessionproc.HelperSpec{
		Path: cfg.Helper[0], Args: cfg.Helper, Env: environment(s.user, s.setup.GetEnv()),
		Files: child, Cred: cfg.Credential(s.user), Ctty: s.ctl != nil,
	}
	var startErr error
	holdErr := cfg.Users.Hold(s.user, func() {
		s.helper, startErr = sessionproc.StartHelper(spec)
	})
	closeAll(child...)
	if rule := startRule(holdErr, startErr); rule != "" {
		s.log.LogAttrs(ctx, slog.LevelWarn, "shell start refused",
			slog.String("rule", rule), slog.String("decision", "refuse"), slog.Any("error", errors.Join(holdErr, startErr)))
		s.closeEnds()
		return rule
	}
	s.sid.Store(sessionproc.PID(s.helper.Pid))
	go func() {
		_, _ = s.helper.Wait()
		close(s.gone)
	}()
	s.log.LogAttrs(ctx, slog.LevelInfo, "shell started",
		slog.Int("helper_pid", s.helper.Pid), slog.Bool("interactive", s.ctl != nil),
		slog.String("decision", "start"))
	return ""
}

func startRule(holdErr, startErr error) string {
	switch {
	case errors.Is(holdErr, users.ErrUnknown):
		return RuleUserUnknown
	case errors.Is(holdErr, users.ErrLocked):
		return RuleUserLocked
	case holdErr != nil, startErr != nil:
		return RuleStartFailed
	}
	return ""
}

// credential is the owner of the PTY's replica: the user, or wb-guestd
// itself when tests start the helper without a user switch.
func (s *session) credential(cfg Config) sessionproc.Cred {
	if c := cfg.Credential(s.user); c != nil {
		return *c
	}
	return sessionproc.Cred{UID: u32(os.Getuid()), GID: u32(os.Getgid())}
}

// open opens the PTY or the pipes, the report pipe and the command
// pipe, and returns the helper's ends: its descriptors 0 to 4.
func (s *session) open(owner sessionproc.Cred) (childEnds, error) {
	var child childEnds
	fail := func(err error) (childEnds, error) {
		closeAll(child...)
		s.closeEnds()
		return nil, err
	}
	streams, err := s.openStreams(owner)
	child = append(child, streams...)
	if err != nil {
		return fail(err)
	}
	repR, repW, err := os.Pipe()
	if err != nil {
		return fail(fmt.Errorf("shell: the report pipe: %w", err))
	}
	s.reports = repR
	child = append(child, repW)
	cmdR, cmdW, err := os.Pipe()
	if err != nil {
		return fail(fmt.Errorf("shell: the command pipe: %w", err))
	}
	s.commands = cmdW
	return append(child, cmdR), nil
}

// openStreams opens the program's standard streams, and returns the
// helper's ends of them, descriptors 0 to 2. On an error, it returns the
// helper's ends it opened.
func (s *session) openStreams(owner sessionproc.Cred) (childEnds, error) {
	if s.setup.GetInteractive() {
		return s.openPTY(owner)
	}
	return s.openPipes()
}

// openPTY opens the PTY, and a standard error pipe when the setup asks
// for a separate one.
func (s *session) openPTY(owner sessionproc.Cred) (childEnds, error) {
	// checkStart checked the size.
	rows, cols, _ := windowSize(s.setup.GetSize())
	// S13-01-process-hardening, "Limits and environment": the replica
	// belongs to the user, with mode 0600, before the helper starts.
	ctl, replica, err := sessionproc.OpenPTY(owner, rows, cols)
	if err != nil {
		return nil, err
	}
	s.ctl, s.stdin, s.stdout = ctl, ctl, ctl
	if !s.setup.GetSeparateStderr() {
		return childEnds{replica, replica, replica}, nil
	}
	errOut, err := s.openStderr()
	return childEnds{replica, replica, errOut}, err
}

// openPipes opens a pipe for each standard stream.
func (s *session) openPipes() (childEnds, error) {
	in, stdin, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("shell: the standard input pipe: %w", err)
	}
	s.stdin = stdin
	stdout, out, err := os.Pipe()
	if err != nil {
		return childEnds{in}, fmt.Errorf("shell: the standard output pipe: %w", err)
	}
	s.stdout = stdout
	errOut, err := s.openStderr()
	return childEnds{in, out, errOut}, err
}

// openStderr opens the standard error pipe, and returns the helper's
// end.
func (s *session) openStderr() (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("shell: the standard error pipe: %w", err)
	}
	s.stderr = r
	return w, nil
}

// closeEnds closes wb-guestd's ends.
func (s *session) closeEnds() {
	closeAll(s.ctl, s.stdin, s.stdout, s.stderr, s.reports, s.commands)
}
