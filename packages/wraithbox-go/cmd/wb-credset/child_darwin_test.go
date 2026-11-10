//go:build darwin && cgo

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// secret is the value the child is given. Its log may not hold it.
const secret = "secret-CHILD-41d7"

// runChild runs the test binary, which the Go linker signs ad hoc, as
// wb-credset with args, the variables vars, and stdin. It returns the
// exit status and the log.
func runChild(t *testing.T, stdin string, vars []string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append([]string{childEnv + "=1"}, vars...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout %q, want nothing", stdout.String())
	}
	if strings.Contains(stderr.String(), secret) {
		t.Errorf("the log holds the value:\n%s", stderr.String())
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}

// confinedThen checks that the log has sandbox-applied, and after it the
// decision line with rule.
func confinedThen(t *testing.T, logs, rule string) {
	t.Helper()
	applied := strings.Index(logs, `"rule":"sandbox-applied"`)
	if applied < 0 {
		t.Fatalf("the child didn't confine itself:\n%s", logs)
	}
	d, ok := credset.ParseDecision([]byte(logs[applied:]))
	if !ok || d.Rule != rule {
		t.Errorf("after sandbox-applied, decision %+v, want %s:\n%s", d, rule, logs)
	}
}

// TestC05CredsetAdhocWrite runs wb-credset's main, ad-hoc signed, and
// sees it confine itself with the store-only profile, then refuse a
// write with keychain-needs-team before any Keychain item call
// (C05-credentials-and-store, "Builds without a team").
func TestC05CredsetAdhocWrite(t *testing.T) {
	code, logs := runChild(t, secret, nil, "write", "github")
	if code != 1 {
		t.Errorf("exit %d, want 1\n%s", code, logs)
	}
	confinedThen(t, logs, string(storeitem.RuleNeedsTeam))
	if strings.Contains(logs, "not built yet") {
		t.Errorf("a call reached the Keychain item calls:\n%s", logs)
	}
}

// TestC05CredsetAdhocRefused sees the confined main refuse a third
// argument and a variable not on the fixed list with credset-refused.
func TestC05CredsetAdhocRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		vars []string
		args []string
	}{
		"third argument": {nil, []string{"write", "github", secret}},
		"variable":       {[]string{"HOME=/"}, []string{"list", "-"}},
	} {
		t.Run(name, func(t *testing.T) {
			code, logs := runChild(t, secret, tc.vars, tc.args...)
			if code != 1 {
				t.Errorf("exit %d, want 1\n%s", code, logs)
			}
			confinedThen(t, logs, credset.RuleRefused)
		})
	}
}
