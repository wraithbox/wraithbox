//go:build unix

package hostgit

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// TestCommandEnvironment: with GIT_* variables, HOME and XDG_CONFIG_HOME
// set in the caller, git sees exactly the variables of S05-04-naming,
// "Resolving the key", HOME from the user database, and
// core.hooksPath=/dev/null before the caller's arguments. Unix only:
// on Windows, os/exec adds SYSTEMROOT.
func TestCommandEnvironment(t *testing.T) {
	for k, v := range map[string]string{
		"GIT_DIR":               "/elsewhere/.git",
		"GIT_CONFIG_PARAMETERS": "'core.pager'='/tmp/evil'",
		"GIT_CONFIG_COUNT":      "1",
		"GIT_CONFIG_KEY_0":      "core.sshCommand",
		"GIT_CONFIG_VALUE_0":    "/tmp/evil",
		"HOME":                  "/wraithbox-test-not-home",
		"XDG_CONFIG_HOME":       "/wraithbox-test-not-xdg",
	} {
		t.Setenv(k, v)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	bin := fakeGit(t, "env")
	dir := t.TempDir()
	cmd, err := Command(t.Context(), bin, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var got fakeOutput
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("stand-in output %q: %v", out, err)
	}

	home, err := platform.UserHome()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"HOME=" + home, "TMPDIR=" + tmp, "LC_ALL=C"}
	xdg, err := platform.LoginXDGConfigHome(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if xdg != "" {
		want = append(want, "XDG_CONFIG_HOME="+xdg)
	}
	slices.Sort(want)
	slices.Sort(got.Env)
	if !slices.Equal(got.Env, want) {
		t.Errorf("environment = %q, want %q", got.Env, want)
	}

	wantArgs := []string{"-c", "core.hooksPath=/dev/null", "rev-parse", "--git-common-dir"}
	if !slices.Equal(got.Args, wantArgs) {
		t.Errorf("args = %q, want %q", got.Args, wantArgs)
	}
	if resolved, _ := filepath.EvalSymlinks(dir); got.Dir != resolved && got.Dir != dir {
		t.Errorf("dir = %q, want %q", got.Dir, dir)
	}
}

// TestCommandRelativeTMPDIR: a TMPDIR that is not an absolute path is
// refused, not passed on or replaced.
func TestCommandRelativeTMPDIR(t *testing.T) {
	bin := fakeGit(t, "env")
	t.Setenv("TMPDIR", "relative/tmp")
	_, err := Command(t.Context(), bin, "", "version")
	checkRefusal(t, err, RuleEnvUnavailable, []string{"TMPDIR"})
}
