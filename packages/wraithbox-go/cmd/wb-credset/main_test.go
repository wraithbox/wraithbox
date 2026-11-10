package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"
)

// A test that needs wb-credset's process runs the test binary again as
// wb-credset: TestMain runs run instead of the tests when childEnv is
// set. Confinement can't be undone, so it never runs in the test
// process itself. The child hands run its environment without childEnv,
// so run checks only what the test chose.
const childEnv = "WB_CREDSET_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) != "" {
		setLog()
		environ := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, childEnv+"=") })
		os.Exit(run(context.Background(), credset.Process{Args: os.Args[1:], Environ: environ, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
	}
	os.Exit(m.Run())
}

// TestProfileEmbedded sees the compiled-in profile be the store-only
// profile: deny by default, and only the two store services.
func TestProfileEmbedded(t *testing.T) {
	for _, want := range []string{
		"(deny default)",
		`(allow mach-lookup (global-name "com.apple.securityd.xpc" "com.apple.ctkd.token-client"))`,
	} {
		if !strings.Contains(profile.Text, want) {
			t.Errorf("the profile has no %q:\n%s", want, profile.Text)
		}
	}
	var rules []string
	for line := range strings.Lines(profile.Text) {
		if strings.HasPrefix(line, "(allow") {
			rules = append(rules, strings.TrimSpace(line))
		}
	}
	if len(rules) != 1 {
		t.Errorf("the profile allows %v, want only the store services", rules)
	}
}

// failReader fails the test when it is read.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Error("wb-credset read its standard input")
	return 0, errors.New("read")
}

// TestC05CredsetConfineRefused sees wb-credset exit 1 when it can't
// confine itself, before it reads its standard input or logs a
// decision, so it makes no store call (SEC12-least-privilege).
func TestC05CredsetConfineRefused(t *testing.T) {
	defer func(c func(selfsandbox.Profile, map[string]string) error) { confine = c }(confine)
	confine = func(selfsandbox.Profile, map[string]string) error { return errors.New("refused") }
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), credset.Process{Args: []string{credset.OpWrite, "github"}, Stdin: failReader{t}, Stdout: &stdout, Stderr: &stderr})
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if _, ok := credset.ParseDecision(stderr.Bytes()); ok || stdout.Len() != 0 {
		t.Errorf("wb-credset went on after the refusal: stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}
