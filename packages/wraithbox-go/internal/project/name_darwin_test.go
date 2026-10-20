package project

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNameMakesValidBranch: the session branch of a valid name passes
// `git check-ref-format` (S05-04-naming, "Session id and branch").
func TestNameMakesValidBranch(t *testing.T) {
	for _, in := range []string{"foo", "My Project", "a..b", "x.lock", "x@{y}", ".hidden", "a~b^c:d", strings.Repeat("z", 200), "日本"} {
		name := nameFrom(in)
		if err := ValidName(name); err != nil {
			t.Fatalf("ValidName(nameFrom(%q) = %q): %v", in, name, err)
		}
		ref := "refs/heads/wb/" + name + "-20261020-1200-ab3x"
		if out, err := exec.CommandContext(t.Context(), "/usr/bin/git", "check-ref-format", ref).CombinedOutput(); err != nil {
			t.Errorf("git check-ref-format %s: %v %s", ref, err, out)
		}
	}
}
