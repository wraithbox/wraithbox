package guestd_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
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
