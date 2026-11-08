package guestd_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsched"
)

// TestCheckSchedulersClosed: the start step reports allow files that
// passed, and fails closed on ones that didn't, with the rule in the
// log (S13-01-process-hardening, "Cron and at").
func TestCheckSchedulersClosed(t *testing.T) {
	for name, c := range map[string]struct {
		err      error
		want     bool
		decision string
	}{
		"closed": {nil, true, "closed"},
		"denied": {errors.New("create .wb-cron.allow.tmp: operation not permitted"), false, "refuse"},
		"wrong":  {errors.New(`/private/var/at/cron.allow: content "root\nadmin\n", want "root\n"`), false, "refuse"},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			calls := 0
			check := func() error { calls++; return c.err }
			got := guestd.CheckSchedulersClosed(t.Context(), slog.New(slog.NewJSONHandler(&buf, nil)), check)
			if got != c.want || calls != 1 {
				t.Errorf("CheckSchedulersClosed = %v after %d calls, want %v after 1", got, calls, c.want)
			}
			for _, s := range []string{`"rule":"` + guestd.RuleSchedulersClosed + `"`, `"decision":"` + c.decision + `"`} {
				if !strings.Contains(buf.String(), s) {
					t.Errorf("log lacks %s: %s", s, buf.String())
				}
			}
		})
	}
}

// TestCheckSchedulersClean: the start step passes only when the scan
// found nothing. A crontab or at job found, or a scan that failed,
// fails closed, with the rule and the counts in the log
// (S13-01-process-hardening, "Cron and at").
func TestCheckSchedulersClean(t *testing.T) {
	for name, c := range map[string]struct {
		found guestsched.Found
		err   error
		want  bool
		logs  []string
	}{
		"nothing": {guestsched.Found{}, nil, true, []string{`"decision":"clean"`, `"level":"INFO"`}},
		"crontab": {guestsched.Found{Tabs: 1}, nil, false, []string{`"decision":"refuse"`, `"level":"ERROR"`, `"crontabs":1`}},
		"at jobs": {guestsched.Found{Jobs: 2}, nil, false, []string{`"decision":"refuse"`, `"level":"ERROR"`, `"at_jobs":2`}},
		"failed": {
			guestsched.Found{Tabs: 1},
			errors.New("open /private/var/at/jobs: not a directory"), false,
			[]string{`"decision":"refuse"`, `"level":"ERROR"`, `"crontabs":1`, `not a directory`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			calls := 0
			scan := func() (guestsched.Found, error) { calls++; return c.found, c.err }
			got := guestd.CheckSchedulersClean(t.Context(), slog.New(slog.NewJSONHandler(&buf, nil)), scan)
			if got != c.want || calls != 1 {
				t.Errorf("CheckSchedulersClean = %v after %d calls, want %v after 1", got, calls, c.want)
			}
			for _, s := range append(c.logs, `"rule":"`+guestd.RuleSchedulersClean+`"`) {
				if !strings.Contains(buf.String(), s) {
					t.Errorf("log lacks %s: %s", s, buf.String())
				}
			}
		})
	}
}
