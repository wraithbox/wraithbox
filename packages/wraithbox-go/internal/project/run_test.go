package project

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestRunFailedSaysWhy: a run past its deadline says it timed out, and a
// canceled run says it was canceled, not that it timed out.
func TestRunFailedSaysWhy(t *testing.T) {
	g := gitRun{dir: "/r", timeout: 10 * time.Second, args: []string{"rev-parse"}}

	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if r := runFailed(expired, g, errors.New("killed"), nil); r.Rule != RuleGitFailed || !strings.Contains(r.msg, "did not finish within 10s") {
		t.Errorf("runFailed past the deadline = %q", r.msg)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if r := runFailed(canceled, g, errors.New("killed"), nil); r.Rule != RuleGitFailed || !strings.Contains(r.msg, "canceled") {
		t.Errorf("runFailed after a cancel = %q", r.msg)
	}
}
