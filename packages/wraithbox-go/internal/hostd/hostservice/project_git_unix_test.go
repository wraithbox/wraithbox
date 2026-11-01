//go:build unix

package hostservice_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
)

// TestCheckGitRecorded: wb-hostd's git check writes its decision and
// rule to the audit log, accepted or refused (S09-11-audit,
// S08-08-host-git).
func TestCheckGitRecorded(t *testing.T) {
	git := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\necho 'git version 2.54.0'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, setting, msg string
		rule               hostgit.Rule
	}{
		{"accepted", git, "host git accept", hostgit.RuleVersionAccepted},
		{"not absolute", "git", "host git refuse", hostgit.RulePathNotAbsolute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &audittest.Recorder{}
			g := hostservice.CheckGit(t.Context(), tc.setting, rec)
			if g.GetRule() != string(tc.rule) {
				t.Fatalf("rule %s, want %s", g.GetRule(), tc.rule)
			}
			recs := rec.Records()
			if len(recs) != 1 || recs[0].Message() != tc.msg || recs[0].ClassUID() != 5019 {
				t.Fatalf("records %q, want one 5019 %q", rec.Messages(), tc.msg)
			}
			f := recs[0].Fields()
			if f.Rule != string(tc.rule) || f.Git == nil || f.Git.Minimum != hostgit.MinVersion {
				t.Errorf("record fields %+v, git %+v", f, f.Git)
			}
		})
	}
}
