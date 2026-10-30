// Package shell is the guest end of `wb shell`: GuestService.Shell, the
// PTY or pipes, and the session helper that runs the program as the
// project user (S06-09-session-lifecycle, "Session helper";
// S06-12-guest-agent, "What it does").
//
// wb-guestd runs as root. Everything a shell sends it is guest input: the
// host's frames through wb-hostd, and the helper's reports, which come
// from the project user's own process (SEC11-root-gains-nothing). Every
// refusal and failure has a rule, which goes to the log and to the host.
package shell

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// KillDelay is how long a shell has to end after a hangup before
// wb-guestd kills its processes (S06-09-session-lifecycle, "Closing the
// terminal ends the session").
const KillDelay = 10 * time.Second

// Users is what a shell start needs of the project users. *users.Manager
// implements it.
type Users interface {
	KnownByName(name string) (users.User, bool)
	Privileged(ctx context.Context, u users.User) (bool, error)
	Hold(u users.User, start func()) error
}

// Stopper is the stop loop of S13-01-process-hardening, "When the link
// drops". *procs.Loops implements it.
type Stopper interface {
	// Idle returns once no stop loop runs.
	Idle()
	// Forget drops processes from the set the stop loop stopped.
	Forget(keys []gp.Key)
}

// Stream is the server side of a Shell call.
type Stream interface {
	Context() context.Context
	Send(*guestv1.ShellResponse) error
	Recv() (*guestv1.ShellRequest, error)
}

// Config is what a Server works with.
type Config struct {
	Log     *slog.Logger
	Users   Users
	Table   sessionproc.Table
	Stopper Stopper
	// MountCheck checks the data volume's mount, and returns the rule of
	// a refusal, or "". It logs its own refusal.
	MountCheck func(ctx context.Context) string
	// Dropped reports whether a stream with the context ended because
	// the link to the host dropped.
	Dropped func(ctx context.Context) bool
	// Helper is the session helper's path and whole argument list.
	Helper []string
	// Credential is the user a helper runs as. nil runs it as wb-guestd,
	// which only tests do.
	Credential func(users.User) *sessionproc.Cred
	// KillDelay is the wait between a hangup and the kill.
	KillDelay time.Duration
}

// UserCredential is the credential of a project user: its UID, and its
// group as primary and only group.
func UserCredential(u users.User) *sessionproc.Cred {
	return &sessionproc.Cred{UID: u.UID, GID: u.GID, Groups: []uint32{u.GID}}
}

// Server runs shells.
type Server struct {
	cfg Config

	mu   sync.Mutex
	open map[string]*session
}

// New returns a Server.
func New(cfg Config) *Server {
	return &Server{cfg: cfg, open: map[string]*session{}}
}

// Shell runs one shell, and returns when it has ended. It returns a
// *Refusal when it refused the start, before any frame.
func (s *Server) Shell(stream Stream) error {
	ctx := stream.Context()
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	start := req.GetStart()
	id, name := start.GetShellId(), start.GetUser()
	if start == nil {
		return s.refuse(ctx, id, name, RuleStartInvalid, true)
	}
	if rule := checkStart(id, name, start.GetSetup()); rule != "" {
		return s.refuse(ctx, id, name, rule, true)
	}
	// Nothing runs as a project user without a checked data mount
	// (guest.proto, MountData; S06-05-disks, "Mounting the data volume").
	if rule := s.cfg.MountCheck(ctx); rule != "" {
		return s.refuse(ctx, id, name, rule, false)
	}
	u, rule := s.user(ctx, name)
	if rule != "" {
		return s.refuse(ctx, id, name, rule, false)
	}
	sn, rule := s.reserve(id, u, start.GetSetup())
	if rule != "" {
		return s.refuse(ctx, id, name, rule, false)
	}
	if rule := sn.start(ctx, s.cfg); rule != "" {
		s.release(sn)
		return s.refuse(ctx, id, name, rule, false)
	}
	sn.run(stream)
	return nil
}

// refuse logs a refused start, and returns it.
func (s *Server) refuse(ctx context.Context, id, user, rule string, invalid bool) error {
	s.cfg.Log.LogAttrs(ctx, slog.LevelWarn, "shell refused",
		slog.String("shell_id", quoted(id)), slog.String("user", quoted(user)),
		slog.String("rule", rule), slog.String("decision", "refuse"))
	return &Refusal{Rule: rule, Invalid: invalid}
}

// user returns the project user named name, or the rule that refuses
// it (SEC08-proj-isolation; SEC12-least-privilege: never root, admin or
// wheel).
func (s *Server) user(ctx context.Context, name string) (users.User, string) {
	u, ok := s.cfg.Users.KnownByName(name)
	if !ok {
		return users.User{}, RuleUserUnknown
	}
	if u.UID == 0 {
		return users.User{}, RuleUserPrivileged
	}
	privileged, err := s.cfg.Users.Privileged(ctx, u)
	if err != nil {
		s.cfg.Log.LogAttrs(ctx, slog.LevelWarn, "shell user check failed",
			slog.String("user", quoted(name)), slog.String("rule", users.RuleDirectory), slog.String("error", err.Error()))
		return users.User{}, users.RuleDirectory
	}
	if privileged {
		return users.User{}, RuleUserPrivileged
	}
	return u, ""
}

// reserve claims the shell id.
func (s *Server) reserve(id string, u users.User, setup *terminalv1.Setup) (*session, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.open[id]; ok {
		return nil, RuleIDInUse
	}
	sn := newSession(s, id, u, setup)
	s.open[id] = sn
	return sn, ""
}

// release ends the claim of a shell's id.
func (s *Server) release(sn *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[sn.id] == sn {
		delete(s.open, sn.id)
	}
}

// openSessions returns the POSIX session IDs of the open shells.
func (s *Server) openSessions() map[int32]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sids := map[int32]bool{}
	for _, sn := range s.open {
		if sid := sn.sid.Load(); sid != 0 {
			sids[sid] = true
		}
	}
	return sids
}

// quoted escapes and cuts guest text for a log line.
func quoted(s string) string {
	const most = 128
	if len(s) > most {
		s = s[:most]
	}
	return strconv.Quote(s)
}

// closeAll closes each file that is set.
func closeAll(fs ...*os.File) {
	for _, f := range fs {
		if f != nil {
			_ = f.Close()
		}
	}
}
