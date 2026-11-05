package policyfile

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestMain keeps the loader's log out of the test output. TestLoadLogs
// checks it.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// TestLoadLogs checks that a refusal and an acceptance are logged with
// the decision, and a refusal with its rule.
func TestLoadLogs(t *testing.T) {
	var buf bytes.Buffer
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(before) })

	loadString(t, global, "version: 2\n")
	out := buf.String()
	for _, want := range []string{"decision=refuse", "rule=" + RuleVersion, "file=" + testFile, "line=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal log %q lacks %q", out, want)
		}
	}

	buf.Reset()
	mustLoad(t, global, "version: 1\n")
	if out := buf.String(); !strings.Contains(out, "decision=accept") || !strings.Contains(out, "source=global") {
		t.Errorf("acceptance log %q lacks the decision or the source", out)
	}
}
