package guestd

import (
	"context"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsched"
)

// RuleSchedulersLock: wb-guestd couldn't write cron's and at's allow
// files with root only at its start, so it doesn't serve the host
// (S13-01-process-hardening, "Cron and at").
const RuleSchedulersLock = "guest-schedulers-lock"

// RuleSchedulersSweep: at its start, wb-guestd removes every crontab and
// at job a project user left from before, or doesn't serve the host
// (S13-01-process-hardening, "Cron and at").
const RuleSchedulersSweep = "guest-schedulers-sweep"

// LockSchedulers runs lock, which writes cron's and at's allow files
// with root only, so no project user can add a crontab or an at job
// (S13-01-process-hardening, "Cron and at"; SEC08-proj-isolation).
// wb-guestd runs it at each start, before it serves the host, so before
// it makes or unlocks any project user. main passes guestsched.Lock.
// It logs the decision, and returns false when lock failed: wb-guestd
// then exits without serving, and fails closed.
func LockSchedulers(ctx context.Context, log *slog.Logger, lock func() error) bool {
	if err := lock(); err != nil {
		log.LogAttrs(ctx, slog.LevelError, "cron and at not closed to project users",
			slog.String("rule", RuleSchedulersLock), slog.String("decision", "refuse"),
			slog.String("error", err.Error()))
		return false
	}
	log.LogAttrs(ctx, slog.LevelInfo, "cron and at closed to project users",
		slog.String("rule", RuleSchedulersLock), slog.String("decision", "locked"))
	return true
}

// SweepSchedulers runs sweep, which removes the crontab of every project
// user and every at job a project user's ID owns. A guest made before
// the lock, or a kill loop that stopped short, can have left one, and
// cron or atrun would start it as its user outside any launchd domain
// (S13-01-process-hardening, "Cron and at"; SEC08-proj-isolation).
// wb-guestd runs it at each start, after LockSchedulers and before it
// serves the host. main passes guestsched.Sweep. It logs the decision,
// and returns false when sweep failed: wb-guestd then exits without
// serving, and fails closed.
func SweepSchedulers(ctx context.Context, log *slog.Logger, sweep func() (guestsched.Swept, error)) bool {
	s, err := sweep()
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "project users' crontabs or at jobs left",
			slog.String("rule", RuleSchedulersSweep), slog.String("decision", "refuse"),
			slog.Int("tabs", s.Tabs), slog.Int("jobs", s.Jobs),
			slog.String("error", err.Error()))
		return false
	}
	level := slog.LevelInfo
	if s != (guestsched.Swept{}) {
		level = slog.LevelWarn
	}
	log.LogAttrs(ctx, level, "no project user's crontab or at job left",
		slog.String("rule", RuleSchedulersSweep), slog.String("decision", "swept"),
		slog.Int("tabs", s.Tabs), slog.Int("jobs", s.Jobs))
	return true
}
