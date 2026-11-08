package guestd

import (
	"context"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsched"
)

// RuleSchedulersClosed: at its start, wb-guestd found cron's or at's
// allow file missing or other than root only, so it doesn't serve the
// host (S13-01-process-hardening, "Cron and at").
const RuleSchedulersClosed = "guest-schedulers-closed"

// RuleSchedulersClean: at its start, wb-guestd found a crontab or an at
// job of a project user, or couldn't look, so it doesn't serve the host
// (S13-01-process-hardening, "Cron and at").
const RuleSchedulersClean = "guest-schedulers-clean"

// CheckSchedulersClosed runs check, which checks that cron's and at's
// allow files name root only, so no project user can add a crontab or
// an at job (S13-01-process-hardening, "Cron and at";
// SEC08-proj-isolation). The image build writes the files, because TCC
// keeps wb-guestd from writing there. wb-guestd runs it at each start,
// before it serves the host, so before it makes or unlocks any project
// user. main passes guestsched.CheckAllow. It logs the decision, and
// returns false when the check failed: wb-guestd then exits without
// serving, and fails closed.
func CheckSchedulersClosed(ctx context.Context, log *slog.Logger, check func() error) bool {
	if err := check(); err != nil {
		log.LogAttrs(ctx, slog.LevelError, "cron and at not closed to project users",
			slog.String("rule", RuleSchedulersClosed), slog.String("decision", "refuse"),
			slog.String("error", err.Error()))
		return false
	}
	log.LogAttrs(ctx, slog.LevelInfo, "cron and at closed to project users",
		slog.String("rule", RuleSchedulersClosed), slog.String("decision", "closed"))
	return true
}

// CheckSchedulersClean runs scan, which counts the crontabs of project
// users and the at jobs a project user's ID owns. cron or atrun would
// start one as its user outside any launchd domain, and wb-guestd can't
// remove it (S13-01-process-hardening, "Cron and at";
// SEC08-proj-isolation). wb-guestd runs it at each start, after
// CheckSchedulersClosed and before it serves the host. main passes
// guestsched.Scan. It logs the decision, and returns false when scan
// found one or failed: wb-guestd then exits without serving, and fails
// closed.
func CheckSchedulersClean(ctx context.Context, log *slog.Logger, scan func() (guestsched.Found, error)) bool {
	f, err := scan()
	attrs := []slog.Attr{
		slog.String("rule", RuleSchedulersClean), slog.Int("crontabs", f.Tabs), slog.Int("at_jobs", f.Jobs),
	}
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "project users' crontabs and at jobs not checked",
			append(attrs, slog.String("decision", "refuse"), slog.String("error", err.Error()))...)
		return false
	}
	if f.Any() {
		log.LogAttrs(ctx, slog.LevelError, "project users' crontabs or at jobs left",
			append(attrs, slog.String("decision", "refuse"))...)
		return false
	}
	log.LogAttrs(ctx, slog.LevelInfo, "no project user's crontab or at job left",
		append(attrs, slog.String("decision", "clean"))...)
	return true
}
