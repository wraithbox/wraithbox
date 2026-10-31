package projectcmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseShell(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		project string
		err     string
	}{
		{"none", nil, "", ""},
		{"project", []string{"--project", "p"}, "p", ""},
		{"project=", []string{"--project=p"}, "p", ""},
		{"command", []string{"ls"}, "", "takes no argument"},
		{"after project", []string{"--project", "p", "ls"}, "", "takes no argument"},
		{"flag", []string{"-l"}, "", "takes no argument"},
		{"no value", []string{"--project"}, "", "needs a project"},
		{"twice", []string{"--project", "a", "--project=b"}, "", "--project"},
		{"empty", []string{"--project="}, "", "--project"},
	} {
		inv, err := parseShell(tc.args)
		if tc.err == "" {
			if err != nil || inv.project != tc.project {
				t.Errorf("%s: parseShell = %+v, %v, want project %q", tc.name, inv, err, tc.project)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: parseShell error %v, want one that says %q", tc.name, err, tc.err)
		}
	}
}

// TestRunShellUsage: an extra argument is a usage error, exit 2, before
// wb looks for its paths or the terminal.
func TestRunShellUsage(t *testing.T) {
	var stderr bytes.Buffer
	code := RunShell(t.Context(), Env{Stderr: &stderr}, ".", []string{"ls"})
	if code != exitUsage {
		t.Errorf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "Usage: wb shell [--project P]") {
		t.Errorf("stderr %q misses the usage", stderr.String())
	}
}
