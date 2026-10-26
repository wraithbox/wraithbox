package hostgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// TestCheckVersionFakes is the C08-git case for the check itself: a
// stand-in below the minimum and one that reports a release candidate of
// the minimum are refused, each with its rule and a message that names
// the binary, its version and the minimum.
func TestCheckVersionFakes(t *testing.T) {
	tests := []struct {
		mode        string
		rule        Rule
		version     string
		wantInError []string
	}{
		{"ok", RuleVersionAccepted, "2.54.0", nil},
		{"old", RuleVersionTooOld, "2.53.9", []string{"2.53.9", MinVersion}},
		{"rc", RuleVersionUnreadable, "", []string{"2.54.0.rc1", MinVersion}},
		{"fail", RuleRunFailed, "", []string{"exit status 1", "invalid active developer path", MinVersion}},
		{"flood", RuleVersionUnreadable, "", []string{strings.Repeat("v", maxShown), MinVersion}},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			bin := fakeGit(t, tt.mode)
			res, err := CheckVersion(t.Context(), bin)
			if res.Rule != tt.rule || res.Binary != bin || res.Version != tt.version {
				t.Errorf("Result = %+v, want rule %s, binary %s, version %q", res, tt.rule, bin, tt.version)
			}
			if tt.rule == RuleVersionAccepted {
				if err != nil {
					t.Fatalf("CheckVersion: %v", err)
				}
				return
			}
			checkRefusal(t, err, tt.rule, append(tt.wantInError, bin))
		})
	}
}

// TestCheckVersionDeadline: a stand-in that never exits is refused with
// the run rule when the deadline passes, and the check returns then.
func TestCheckVersionDeadline(t *testing.T) {
	bin := fakeGit(t, "hang")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	res, err := CheckVersion(ctx, bin)
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("CheckVersion took %s, want it to end at the deadline", took)
	}
	if res.Rule != RuleRunFailed {
		t.Errorf("rule = %s, want %s", res.Rule, RuleRunFailed)
	}
	checkRefusal(t, err, RuleRunFailed, []string{bin, "timed out", MinVersion})
}

// TestCheckVersionMissing: a binary that doesn't exist is refused with
// the run rule. At the platform default the message says how to fix it.
func TestCheckVersionMissing(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "no-such-git")
	_, err := CheckVersion(t.Context(), bin)
	checkRefusal(t, err, RuleRunFailed, []string{bin, MinVersion})
	if strings.Contains(err.Error(), platform.GitInstallHint()) {
		t.Errorf("hint for a path that is not the default: %v", err)
	}

	r := runFailed(t.Context(), platform.DefaultGitPath(), errors.New("no such file"), nil)
	if !strings.Contains(r.Error(), platform.GitInstallHint()) {
		t.Errorf("no hint at the default path: %v", r)
	}
}

// TestCheckVersionContextEnded: a check the caller's context ended says
// so, and gives no install hint, even at the default path.
func TestCheckVersionContextEnded(t *testing.T) {
	bin := fakeGit(t, "hang")
	ctx, cancel := context.WithCancel(t.Context())
	timer := time.AfterFunc(time.Second, cancel)
	defer timer.Stop()
	_, err := CheckVersion(ctx, bin)
	checkRefusal(t, err, RuleRunFailed, []string{bin, "canceled", MinVersion})

	expired, stop := context.WithTimeout(t.Context(), 0)
	defer stop()
	<-expired.Done()
	r := runFailed(expired, platform.DefaultGitPath(), errors.New("signal: killed"), nil)
	if !strings.Contains(r.Error(), "timed out") || strings.Contains(r.Error(), platform.GitInstallHint()) {
		t.Errorf("message %q: want a timeout and no install hint", r)
	}
}

func TestCheckVersionRelativePath(t *testing.T) {
	_, err := CheckVersion(t.Context(), "git")
	checkRefusal(t, err, RulePathNotAbsolute, []string{"git", MinVersion})
}

// TestLoginLookupFailure: when the login environment can't be read, as
// when launchctl exits nonzero or prints too much (platform's
// TestLoginEnvFailures), git doesn't run, and Command and CheckVersion
// refuse with git-env-unavailable. Never a fallback to the caller's
// XDG_CONFIG_HOME (SEC12-least-privilege).
func TestLoginLookupFailure(t *testing.T) {
	for _, cause := range []error{
		errors.New("/bin/launchctl getenv XDG_CONFIG_HOME: exit status 3"),
		errors.New("/bin/launchctl getenv XDG_CONFIG_HOME: output longer than 4096 bytes"),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			saved := loginXDGConfigHome
			t.Cleanup(func() { loginXDGConfigHome = saved })
			loginXDGConfigHome = func(context.Context) (string, error) { return "", cause }

			bin := fakeGit(t, "spy")
			_, err := Command(t.Context(), bin, "", "version")
			checkRefusal(t, err, RuleEnvUnavailable, []string{bin, "XDG_CONFIG_HOME", MinVersion})
			if !errors.Is(err, cause) {
				t.Errorf("err = %v, want it to wrap %v", err, cause)
			}
			res, err := CheckVersion(t.Context(), bin)
			checkRefusal(t, err, RuleEnvUnavailable, []string{bin, MinVersion})
			if res.Rule != RuleEnvUnavailable {
				t.Errorf("rule = %s, want %s", res.Rule, RuleEnvUnavailable)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(bin), "ran")); err == nil {
				t.Error("git ran without its environment")
			}
		})
	}
}

func checkRefusal(t *testing.T, err error, rule Rule, inMessage []string) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a *Refusal", err)
	}
	if r.Rule != rule {
		t.Errorf("rule = %s, want %s", r.Rule, rule)
	}
	for _, s := range inMessage {
		if !strings.Contains(r.Error(), s) {
			t.Errorf("message %q does not name %q", r.Error(), s)
		}
	}
}

// TestResolve: an empty setting gives the platform default, an absolute
// one is used, and a relative one is refused (S08-08-host-git, "Which
// git").
func TestResolve(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "git")
	tests := []struct {
		setting string
		want    string
		refused bool
	}{
		{"", platform.DefaultGitPath(), false},
		{abs, abs, false},
		{"git", "", true},
		{filepath.Join("bin", "git"), "", true},
		{"~/bin/git", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.setting, func(t *testing.T) {
			got, err := Resolve(tt.setting)
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.setting, got, tt.want)
			}
			if !tt.refused {
				if err != nil {
					t.Errorf("Resolve(%q): %v", tt.setting, err)
				}
				return
			}
			checkRefusal(t, err, RulePathNotAbsolute, []string{tt.setting, MinVersion, platform.DefaultGitPath()})
		})
	}
}

// TestResolveNeverUsesPATH puts a git stand-in first in PATH. Resolve
// doesn't return it, and neither a check of the resolved binary nor a
// relative path given to Command runs it (SEC03-no-host-exec).
func TestResolveNeverUsesPATH(t *testing.T) {
	dir := t.TempDir()
	spy := install(t, dir, "git")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := Resolve("")
	if err != nil || got != platform.DefaultGitPath() || got == spy {
		t.Fatalf("Resolve(\"\") = %q, %v; want %q", got, err, platform.DefaultGitPath())
	}
	// The host's git may be missing or too old: only that the spy
	// didn't run matters here.
	_, _ = CheckVersion(t.Context(), got)
	if _, err := Command(t.Context(), "git", dir, "version"); err == nil {
		t.Error("Command with a relative path: want a refusal")
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Error("the git stand-in in PATH ran")
	}
}

// TestCheck: Check refuses a relative setting with the path rule before
// it runs anything, and otherwise checks the version of the binary the
// setting names.
func TestCheck(t *testing.T) {
	res, err := Check(t.Context(), "git")
	checkRefusal(t, err, RulePathNotAbsolute, []string{"git"})
	if res.Rule != RulePathNotAbsolute {
		t.Errorf("Result = %+v", res)
	}
	bin := fakeGit(t, "old")
	_, err = Check(t.Context(), bin)
	checkRefusal(t, err, RuleVersionTooOld, []string{bin, MinVersion})
	res, err = Check(t.Context(), fakeGit(t, "ok"))
	if err != nil || res.Rule != RuleVersionAccepted {
		t.Errorf("Check(ok) = %+v, %v", res, err)
	}
}
