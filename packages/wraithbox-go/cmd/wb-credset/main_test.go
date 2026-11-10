package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
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
		os.Exit(run(context.Background(), os.Args[1:], environ))
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
