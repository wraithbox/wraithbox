package codesign

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadTeamSystemProgram reads the signature of a program Apple
// signs, which has no team.
func TestReadTeamSystemProgram(t *testing.T) {
	team, err := ReadTeam(t.Context(), "/bin/ls")
	if err != nil || team != "" {
		t.Errorf("ReadTeam(/bin/ls) = %q, %v; want no team", team, err)
	}
}

// TestReadTeamUnsigned fails on a file that isn't signed.
func TestReadTeamUnsigned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(path, []byte("not a program"), 0o600); err != nil {
		t.Fatal(err)
	}
	if team, err := ReadTeam(t.Context(), path); err == nil {
		t.Errorf("ReadTeam of an unsigned file = %q, want an error", team)
	}
}

// TestReadProfileNotAProfile fails on a file that isn't a signed
// profile, such as the decoded form itself, and on a missing one.
func TestReadProfileNotAProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embedded.provisionprofile")
	if err := os.WriteFile(path, []byte(testProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + ".missing"} {
		if _, err := ReadProfile(t.Context(), p); err == nil {
			t.Errorf("ReadProfile(%s) succeeded", p)
		}
	}
}
