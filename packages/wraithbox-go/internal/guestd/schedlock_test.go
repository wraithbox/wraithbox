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

// TestLockSchedulers: the start step reports a lock that held, and
// fails closed on one that didn't, with the rule in the log
// (S13-01-process-hardening, "Cron and at").
func TestLockSchedulers(t *testing.T) {
	for name, c := range map[string]struct {
		err      error
		want     bool
		decision string
	}{
		"locked": {nil, true, "locked"},
		"failed": {errors.New("open /private/var/at: permission denied"), false, "refuse"},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			calls := 0
			lock := func() error { calls++; return c.err }
			got := guestd.LockSchedulers(t.Context(), slog.New(slog.NewJSONHandler(&buf, nil)), lock)
			if got != c.want || calls != 1 {
				t.Errorf("LockSchedulers = %v after %d calls, want %v after 1", got, calls, c.want)
			}
			for _, s := range []string{`"rule":"` + guestd.RuleSchedulersLock + `"`, `"decision":"` + c.decision + `"`} {
				if !strings.Contains(buf.String(), s) {
					t.Errorf("log lacks %s: %s", s, buf.String())
				}
			}
		})
	}
}

// TestSweepSchedulers: the start step reports a sweep that finished,
// with what it removed, and fails closed on one that didn't, with the
// rule in the log (S13-01-process-hardening, "Cron and at").
func TestSweepSchedulers(t *testing.T) {
	for name, c := range map[string]struct {
		swept guestsched.Swept
		err   error
		want  bool
		logs  []string
	}{
		"nothing": {guestsched.Swept{}, nil, true, []string{`"decision":"swept"`, `"level":"INFO"`}},
		"removed": {guestsched.Swept{Tabs: 1, Jobs: 2}, nil, true, []string{`"decision":"swept"`, `"level":"WARN"`, `"tabs":1`, `"jobs":2`}},
		"failed": {
			guestsched.Swept{Tabs: 1},
			errors.New("open /private/var/at/jobs: not a directory"), false,
			[]string{`"decision":"refuse"`, `"level":"ERROR"`, `"tabs":1`, `not a directory`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			calls := 0
			sweep := func() (guestsched.Swept, error) { calls++; return c.swept, c.err }
			got := guestd.SweepSchedulers(t.Context(), slog.New(slog.NewJSONHandler(&buf, nil)), sweep)
			if got != c.want || calls != 1 {
				t.Errorf("SweepSchedulers = %v after %d calls, want %v after 1", got, calls, c.want)
			}
			for _, s := range append(c.logs, `"rule":"`+guestd.RuleSchedulersSweep+`"`) {
				if !strings.Contains(buf.String(), s) {
					t.Errorf("log lacks %s: %s", s, buf.String())
				}
			}
		})
	}
}
