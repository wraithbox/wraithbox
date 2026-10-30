package shell

import (
	"context"
	"log/slog"
	"time"

	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// The bounds of the kill (S13-01-process-hardening, "When a session's
// client goes", step 2).
const (
	// killRounds is how often the kill lists the session again, for
	// processes that forked meanwhile.
	killRounds = 10
	// killPause is the wait between the rounds, and between the checks
	// of waitForEnd.
	killPause = 50 * time.Millisecond
)

// members lists the processes of the shell's POSIX session that run as
// its user, by effective and real user ID. Both come from the kernel,
// never from a report. A process of the session with another user ID is
// logged and left (SEC08-proj-isolation: a shell touches no other
// user's processes).
func (s *session) members(ctx context.Context) []sessionproc.Proc {
	uid, sid := s.user.UID, s.sid.Load()
	all, err := s.srv.cfg.Table.ByUID(uid)
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "shell processes not listed",
			slog.String("rule", RuleKilled), slog.String("error", err.Error()))
		return nil
	}
	var out []sessionproc.Proc
	for _, p := range all {
		switch {
		case p.SID != sid || sid < 2:
		case p.UID == uid && p.RUID == uid:
			out = append(out, p)
		default:
			s.log.LogAttrs(ctx, slog.LevelWarn, "shell process of another user left",
				procAttrs(p, RuleKilled, "leave-other-user")...)
		}
	}
	return out
}

// kill sends SIGKILL to the members of the shell's session, round after
// round while any is left, and then logs each process of the user that
// is in no open shell's session (S13-01-process-hardening, "When a
// session's client goes", steps 2 and 4).
func (s *session) kill() {
	ctx := context.Background()
	for range killRounds {
		ps := s.members(ctx)
		if len(ps) == 0 {
			break
		}
		keys := make([]gp.Key, 0, len(ps))
		for _, p := range ps {
			keys = append(keys, p.Key())
		}
		s.srv.cfg.Stopper.Forget(keys)
		for _, p := range ps {
			attrs := procAttrs(p, RuleKilled, "kill")
			if err := s.srv.cfg.Table.Kill(p); err != nil {
				attrs = append(attrs, slog.String("error", err.Error()))
			}
			s.log.LogAttrs(ctx, slog.LevelWarn, "shell process killed", attrs...)
		}
		time.Sleep(killPause)
	}
	s.logOutside(ctx)
}

// logOutside logs each process of the user that is in no open shell's
// POSIX session, and leaves it.
func (s *session) logOutside(ctx context.Context) {
	all, err := s.srv.cfg.Table.ByUID(s.user.UID)
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "shell processes not listed",
			slog.String("rule", RuleOutsideSession), slog.String("error", err.Error()))
		return
	}
	open := s.srv.openSessions()
	for _, p := range all {
		if p.SID == s.sid.Load() || open[p.SID] {
			continue
		}
		s.log.LogAttrs(ctx, slog.LevelWarn, "process outside every shell session",
			procAttrs(p, RuleOutsideSession, "leave")...)
	}
}

// waitForEnd waits up to delay for the shell's POSIX session to have no
// member left, and reports whether members are left after it.
func (s *session) waitForEnd(delay time.Duration) bool {
	ctx := context.Background()
	deadline := time.Now().Add(delay)
	for time.Now().Before(deadline) {
		if len(s.members(ctx)) == 0 {
			return false
		}
		time.Sleep(killPause)
	}
	return true
}

// forget drops the session's processes from the stop loop's set, once
// the helper was told to send them SIGCONT: they no longer stay stopped
// (S13-01-process-hardening, "When the link drops").
func (s *session) forget() {
	ps := s.members(context.Background())
	keys := make([]gp.Key, 0, len(ps))
	for _, p := range ps {
		keys = append(keys, p.Key())
	}
	s.srv.cfg.Stopper.Forget(keys)
}

func procAttrs(p sessionproc.Proc, rule, decision string) []slog.Attr {
	return []slog.Attr{
		slog.Int("pid", int(p.PID)), slog.Int("sid", int(p.SID)),
		slog.Uint64("uid", uint64(p.UID)), slog.Uint64("ruid", uint64(p.RUID)),
		slog.String("name", quoted(p.Name)), slog.String("rule", rule), slog.String("decision", decision),
	}
}
