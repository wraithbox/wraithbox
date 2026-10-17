package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"go-build"},
		{"check", "-app", "x"},
		{"install", "-app", "x"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(t.Context(), args, &stdout, &stderr); code != 2 {
			t.Errorf("run %v = %d, want 2; stderr %s", args, code, stderr.String())
		}
	}
}

// TestC12BundleIdentityCommand: a set WB_TEAM_ID fails the identity
// command, which app:identity runs before any build task.
func TestC12BundleIdentityCommand(t *testing.T) {
	t.Setenv(appbundle.TeamEnv, "ABCDE12345")
	t.Setenv(appbundle.BundleIDEnv, "")
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"identity"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Errorf("identity with %s = %d, stdout %q", appbundle.TeamEnv, code, stdout.String())
	}
	if !strings.Contains(stderr.String(), appbundle.TeamEnv) {
		t.Errorf("the message %q doesn't name %s", stderr.String(), appbundle.TeamEnv)
	}

	t.Setenv(appbundle.TeamEnv, "")
	stdout.Reset()
	if code := run(t.Context(), []string{"identity"}, &stdout, &stderr); code != 0 || stdout.String() != appbundle.AdhocBundleID+"\n" {
		t.Errorf("identity = %d, %q; want %s", code, stdout.String(), appbundle.AdhocBundleID)
	}
}

func TestLayoutCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"layout"}, &stdout, &stderr); code != 0 {
		t.Fatalf("layout = %d: %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if !slices.Contains(lines, "swiftpm macos wb-vmd "+appbundle.VirtualizationEntitlementsFile) ||
		!slices.Contains(lines, "go helper wb-proxyd -") {
		t.Errorf("layout lines:\n%s", stdout.String())
	}
}

func TestReadStamp(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		data string
		ok   bool
	}{
		"good":    {"0.1.0+g0123456789abcdef", true},
		"newline": {"0.1.0+g0123456789abcdef\n", false},
		"empty":   {"", false},
		"form":    {"0.1+g01", false},
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := readStamp(path); (err == nil) != tc.ok || (tc.ok && got != tc.data) {
			t.Errorf("%s: readStamp = %q, %v", name, got, err)
		}
	}
	if _, err := readStamp(filepath.Join(dir, "missing")); err == nil {
		t.Error("readStamp of a missing file succeeded")
	}
}

func TestGoBuildArgs(t *testing.T) {
	got := goBuildArgs("wb", "0.1.0+g0123456789abcdef", "out")
	want := []string{"build", "-trimpath=false", "-ldflags=-X " + version.StampVar + "=0.1.0+g0123456789abcdef", "-o", filepath.Join("out", "wb"), "./cmd/wb"}
	if !slices.Equal(got, want) {
		t.Errorf("goBuildArgs = %q, want %q", got, want)
	}
	if env := goBuildEnv(); !slices.Contains(env, "GOFLAGS=") || !slices.Contains(env, "GOARCH=arm64") {
		t.Errorf("goBuildEnv = %q, want an empty GOFLAGS and GOARCH=arm64", env)
	}
}

func TestHasRuntimeFlag(t *testing.T) {
	for out, want := range map[string]bool{
		"CodeDirectory v=20500 size=4951 flags=0x10002(adhoc,runtime) hashes=148+3 location=embedded": true,
		"CodeDirectory v=20400 size=610 flags=0x20002(adhoc,linker-signed) hashes=16+0":               false,
		"CodeDirectory v=20400 size=3629 flags=0x0(none) hashes=103+7 location=embedded":              false,
		"Identifier=wb\nflags=0x10000(runtime)":                                                       false,
		"":                                                                                            false,
	} {
		if got := hasRuntimeFlag(out); got != want {
			t.Errorf("%q: runtime %v, want %v", out, got, want)
		}
	}
}
