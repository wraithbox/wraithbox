package guestd

import (
	"context"
	"log/slog"
)

// RuleSchedulersLock: wb-guestd couldn't write cron's and at's allow
// files with root only at its start, so it doesn't serve the host
// (S13-01-process-hardening, "Cron and at").
const RuleSchedulersLock = "guest-schedulers-lock"

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
