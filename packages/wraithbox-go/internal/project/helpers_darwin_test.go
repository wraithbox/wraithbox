package project

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

// The tests that run git use /usr/bin/git through hostgit.Resolve(""),
// with no version check, so they pass with the CI runner's git.

// tempDir returns a new temporary directory in canonical form, so
// paths the tests build compare equal to the ones Resolve returns.
func tempDir(t *testing.T) string {
	t.Helper()
	d, err := platform.CanonicalDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// testResolver returns a Resolver whose git reads the test's global
// configuration file, global, instead of the user's, and whose log the
// test can read. Its own directories are under a directory that the
// test doesn't create unless it needs them.
func testResolver(t *testing.T, global string) (*Resolver, *bytes.Buffer) {
	t.Helper()
	git, err := hostgit.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(tempDir(t), "own")
	var log bytes.Buffer
	return &Resolver{
		Git: git,
		Own: paths.Paths{
			Data:   filepath.Join(own, "data"),
			Config: filepath.Join(own, "config"),
			Logs:   filepath.Join(own, "logs"),
		},
		Log:     slog.New(slog.NewJSONHandler(&log, nil)),
		testEnv: []string{"GIT_CONFIG_GLOBAL=" + global, "GIT_CONFIG_NOSYSTEM=1"},
	}, &log
}

// emptyGlobal writes an empty global git configuration.
func emptyGlobal(t *testing.T) string {
	t.Helper()
	p := filepath.Join(tempDir(t), "gitconfig")
	writeFile(t, p, "")
	return p
}

// gitT runs the test's git to build a fixture, in an environment of its
// own, so neither the caller's GIT_* variables nor the user's
// configuration change the fixture.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	home := filepath.Join(os.TempDir(), "wraithbox-project-test-home-absent")
	full := append([]string{
		"-c", "init.defaultBranch=main", "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgSign=false", "-c", "protocol.file.allow=always",
	}, args...)
	cmd := exec.CommandContext(t.Context(), "/usr/bin/git", full...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "PATH=/usr/bin:/bin", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes a repository at dir with one commit.
func newRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "root")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func symlink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

// requireRule: err is a Refusal with rule, and the log has the rule.
func requireRule(t *testing.T, err error, rule Rule, log *bytes.Buffer) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != rule {
		t.Fatalf("err = %v, want rule %s", err, rule)
	}
	if !strings.Contains(log.String(), `"rule":"`+string(rule)+`"`) {
		t.Errorf("log = %s, want rule %s", log, rule)
	}
}

func resolveOK(t *testing.T, r *Resolver, dir string) *Repo {
	t.Helper()
	repo, err := r.Resolve(t.Context(), dir)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", dir, err)
	}
	return repo
}
