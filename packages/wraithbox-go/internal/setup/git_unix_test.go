//go:build unix

package setup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
)

// TestC08SetupGitCheck is the wb setup half of C08-git: with git in
// config.toml set to a git below the minimum, or to a release candidate
// of it, or to a relative path, wb setup is refused with the git rule
// before it writes anything or asks the service manager, and names the
// binary, its version and the minimum (S08-08-host-git, "Where it is
// checked"). A git at the minimum passes the check.
func TestC08SetupGitCheck(t *testing.T) {
	tests := []struct {
		name    string
		version string // what the stand-in prints, "" for a relative setting
		rule    Rule
		says    []string
	}{
		{"old", "git version 2.53.0", Rule(hostgit.RuleVersionTooOld), []string{"2.53.0", hostgit.MinVersion}},
		{"release candidate", "git version 2.54.0.rc1", Rule(hostgit.RuleVersionUnreadable), []string{"2.54.0.rc1", hostgit.MinVersion}},
		{"relative", "", Rule(hostgit.RulePathNotAbsolute), []string{"bin/git", hostgit.MinVersion}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			setting := "bin/git"
			if tc.version != "" {
				setting = filepath.Join(f.root, "bin", "git")
				f.write(setting, "#!/bin/sh\necho '"+tc.version+"'\n")
			}
			f.write(filepath.Join(f.env.Paths.Config, config.FileName), "git = \""+setting+"\"\n")
			f.env.CheckGit = func(ctx context.Context) error { return CheckGitAt(ctx, f.env.Paths.Config) }
			f.wantRefused(tc.rule, f.tree())
			f.wantStderr(append(tc.says, setting)...)
		})
	}

	f := newFixture(t)
	bin := filepath.Join(f.root, "bin", "git")
	f.write(bin, "#!/bin/sh\necho 'git version 2.54.0'\n")
	f.write(filepath.Join(f.env.Paths.Config, config.FileName), "git = \""+bin+"\"\n")
	if err := CheckGitAt(t.Context(), f.env.Paths.Config); err != nil {
		t.Errorf("CheckGitAt with git 2.54.0: %v", err)
	}
}

// TestSetupGitCheckFailsClosed: an Env without a git check, and a
// settings file that can't be read, are refused before anything is
// written.
func TestSetupGitCheckFailsClosed(t *testing.T) {
	f := newFixture(t)
	f.env.CheckGit = nil
	f.wantRefused(RuleGitUnchecked, f.tree())

	f = newFixture(t)
	f.write(filepath.Join(f.env.Paths.Config, config.FileName), "gti = \"/usr/bin/git\"\n")
	f.env.CheckGit = func(ctx context.Context) error { return CheckGitAt(ctx, f.env.Paths.Config) }
	f.wantRefused(Rule(config.RuleKeyUnknown), f.tree())
	f.wantStderr("gti", config.FileName)
}
