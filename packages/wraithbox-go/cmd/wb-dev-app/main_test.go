package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/setup"
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

// TestC12BundleIdentityCommand: a WB_TEAM_ID that isn't a team ID fails
// the identity command, which app:identity runs before any build task,
// and a team ID gives the team's identifier.
func TestC12BundleIdentityCommand(t *testing.T) {
	t.Setenv(appbundle.TeamEnv, "abcde12345")
	t.Setenv(appbundle.BundleIDEnv, "")
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"identity"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Errorf("identity with a lowercase %s = %d, stdout %q", appbundle.TeamEnv, code, stdout.String())
	}
	if !strings.Contains(stderr.String(), appbundle.TeamEnv) {
		t.Errorf("the message %q doesn't name %s", stderr.String(), appbundle.TeamEnv)
	}

	t.Setenv(appbundle.TeamEnv, "ABCDE12345")
	if code := run(t.Context(), []string{"identity"}, &stdout, &stderr); code != 0 || stdout.String() != "nl.wraithbox.dev.abcde12345\n" {
		t.Errorf("identity with a team = %d, %q", code, stdout.String())
	}

	t.Setenv(appbundle.TeamEnv, "")
	stdout.Reset()
	if code := run(t.Context(), []string{"identity"}, &stdout, &stderr); code != 0 || stdout.String() != appbundle.AdhocBundleID+"\n" {
		t.Errorf("identity = %d, %q; want %s", code, stdout.String(), appbundle.AdhocBundleID)
	}
}

// TestMinMacOSCommand: the build's macOS minimum is wb setup's, so the
// app opens on no Mac that wb setup refuses (NFR04-host-platforms).
func TestMinMacOSCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	want := fmt.Sprintf("%d.0\n", setup.MinMacOS)
	if code := run(t.Context(), []string{"min-macos"}, &stdout, &stderr); code != 0 || stdout.String() != want {
		t.Errorf("min-macos = %d, %q; want %q", code, stdout.String(), want)
	}
	if code := run(t.Context(), []string{"min-macos", "extra"}, &stdout, &stderr); code != 2 {
		t.Errorf("min-macos extra = %d, want 2", code)
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

// TestLayoutCommandTeam: with a team, each helper's line names an
// entitlements file with its row's groups, and the other lines stay.
func TestLayoutCommandTeam(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "entitlements")
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"layout", "-team", "ABCDE12345", "-dir", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("layout -team = %d: %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	path := filepath.Join(dir, "wb-credset.entitlements")
	if !slices.Contains(lines, "go helper wb-credset "+path) ||
		!slices.Contains(lines, "swiftpm macos wb-vmd "+appbundle.VirtualizationEntitlementsFile) ||
		!slices.Contains(lines, "go macos wb -") {
		t.Errorf("layout lines:\n%s", stdout.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<string>ABCDE12345.nl.wraithbox.binding</string>") {
		t.Errorf("wb-credset's entitlements:\n%s", data)
	}
	for _, args := range [][]string{
		{"layout", "-team", "ABCDE12345"},
		{"layout", "-dir", dir},
		{"layout", "-team", "abcde12345", "-dir", dir},
	} {
		if code := run(t.Context(), args, &stdout, &stderr); code == 0 {
			t.Errorf("run %v = 0", args)
		}
	}
}

// TestC05BuildMissingProfile: a team build whose app from Xcode has no
// profile, a profile that isn't a regular file, or one of another team
// stops with an error that names WB_TEAM_ID and the one-time setup.
func TestC05BuildMissingProfile(t *testing.T) {
	const team = "ABCDE12345"
	dir := t.TempDir()
	file := filepath.Join(dir, "profile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(teams ...string) func(context.Context, string) (codesign.Profile, error) {
		return func(context.Context, string) (codesign.Profile, error) {
			return codesign.Profile{TeamIdentifier: teams, TeamName: "Test"}, nil
		}
	}
	unreadable := func(context.Context, string) (codesign.Profile, error) {
		return codesign.Profile{}, errors.New("security cms: not a profile")
	}
	for label, tc := range map[string]struct {
		path string
		read func(context.Context, string) (codesign.Profile, error)
	}{
		"no profile":         {filepath.Join(dir, "missing"), read(team)},
		"a directory":        {dir, read(team)},
		"another team":       {file, read("B2B2B2B2B2")},
		"two teams":          {file, read(team, "B2B2B2B2B2")},
		"an unreadable file": {file, unreadable},
	} {
		_, err := checkProfile(t.Context(), tc.path, team, tc.read)
		if !errors.Is(err, appbundle.ErrNoProfile) {
			t.Errorf("%s: checkProfile = %v, want ErrNoProfile", label, err)
			continue
		}
		for _, want := range []string{appbundle.TeamEnv, team, "Missing profile", "Sign in to Xcode"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the message %q doesn't name %q", label, err, want)
			}
		}
	}
	if p, err := checkProfile(t.Context(), file, team, read(team)); err != nil || p.TeamName != "Test" {
		t.Errorf("checkProfile of the team's profile = %+v, %v", p, err)
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
	if env := goBuildEnv(); !slices.Contains(env, "GOFLAGS=") || !slices.Contains(env, "GOARCH=arm64") || !slices.Contains(env, "CGO_ENABLED=1") {
		t.Errorf("goBuildEnv = %q, want an empty GOFLAGS, GOARCH=arm64 and CGO_ENABLED=1", env)
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

// TestReadProfileTeams leaves out a profile that doesn't decode, so the
// check refuses it (profile-team).
func TestReadProfileTeams(t *testing.T) {
	read := func(_ context.Context, path string) (codesign.Profile, error) {
		if strings.Contains(path, "bad") {
			return codesign.Profile{}, errors.New("not a profile")
		}
		return codesign.Profile{TeamIdentifier: []string{"ABCDE12345"}}, nil
	}
	good, bad := "good/"+appbundle.ProfileName, "bad/"+appbundle.ProfileName
	got := readProfileTeams(t.Context(), "/app", []string{good, bad}, read)
	if len(got) != 1 || !slices.Equal(got[good], []string{"ABCDE12345"}) {
		t.Errorf("readProfileTeams = %v, want only the good profile", got)
	}
}
