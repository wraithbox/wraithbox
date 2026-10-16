package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// isolateGit keeps the developer's git configuration (signing, hooks,
// templates) out of the test repositories.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

// newRepo makes a git repository with one commit that holds VERSION, and
// returns its root.
func newRepo(t *testing.T, release string) string {
	t.Helper()
	isolateGit(t)
	root := t.TempDir()
	mustGit(t, root, "init", "--quiet")
	write(t, filepath.Join(root, releaseFile), release)
	write(t, filepath.Join(root, ".gitignore"), "/build/\n")
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "--quiet", "-m", "first")
	return root
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readStampFile(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(stampFile)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runStamp(t *testing.T, dir string) string {
	t.Helper()
	var out bytes.Buffer
	if err := stamp(context.Background(), dir, &out); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	return readStampFile(t, dir)
}

var (
	cleanForm = regexp.MustCompile(`^1\.2\.3\+g[0-9a-f]{16}$`)
	dirtyForm = regexp.MustCompile(`^1\.2\.3\+g[0-9a-f]{16}\.dirty\.[0-9a-f]{16}$`)
)

func TestStamp(t *testing.T) {
	root := newRepo(t, "1.2.3\n")
	head := mustGit(t, root, "rev-parse", "HEAD")

	clean := runStamp(t, root)
	if !cleanForm.MatchString(clean) || !strings.Contains(clean, "+g"+head[:revLen]) {
		t.Fatalf("clean tree stamped %q, want 1.2.3+g%s", clean, head[:revLen])
	}
	if again := runStamp(t, root); again != clean {
		t.Errorf("a clean tree stamped %q, then %q: want the same version", clean, again)
	}

	// An untracked file is a change: it can end up in the build.
	write(t, filepath.Join(root, "new.go"), "package x\n")
	first := runStamp(t, root)
	second := runStamp(t, root)
	for _, v := range []string{first, second} {
		if !dirtyForm.MatchString(v) || !strings.HasPrefix(v, clean+dirtyMark) {
			t.Errorf("modified tree stamped %q, want %s.dirty.<nonce>", v, clean)
		}
	}
	if first == second {
		t.Errorf("two stamps of a modified tree are both %q: want another nonce each", first)
	}
}

func TestStampFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		release string // "" leaves VERSION out of the repository
		wantErr string
	}{
		{name: "no release file", release: "", wantErr: "reading the release number"},
		{name: "build part in the release", release: "1.2.3+x\n", wantErr: "not MAJOR.MINOR.PATCH"},
		{name: "not a release", release: "one\n", wantErr: "not MAJOR.MINOR.PATCH"},
		{name: "two lines", release: "1.2.3\n\n", wantErr: "not MAJOR.MINOR.PATCH"},
		{name: "version too long", release: "1.2.3-" + strings.Repeat("a", 50) + "\n", wantErr: "not in the version form"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t, tt.release)
			if tt.release == "" {
				mustGit(t, root, "rm", "--quiet", releaseFile)
				mustGit(t, root, "commit", "--quiet", "-m", "no release")
			}
			err := stamp(context.Background(), root, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("stamp() = %v, want an error with %q", err, tt.wantErr)
			}
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(stampFile))); statErr == nil {
				t.Errorf("stamp() failed and still wrote %s", stampFile)
			}
		})
	}
}

func TestStampOutsideARepository(t *testing.T) {
	isolateGit(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	if err := stamp(context.Background(), t.TempDir(), &bytes.Buffer{}); err == nil {
		t.Fatal("stamp() outside a git repository succeeded, want an error")
	}
}

func TestMatchTree(t *testing.T) {
	const rev = "0123456789abcdef"
	tests := []struct {
		name    string
		stamp   string
		tree    tree
		wantErr string // "" for a match
	}{
		{name: "clean stamp, clean tree", stamp: "1.2.3+g" + rev, tree: tree{rev: rev}},
		{name: "modified stamp, modified tree", stamp: "1.2.3+g" + rev + ".dirty.00ff", tree: tree{rev: rev, dirty: true}},
		{name: "modified stamp, tree committed since", stamp: "1.2.3+g" + rev + ".dirty.00ff", tree: tree{rev: rev}},
		{name: "another commit", stamp: "1.2.3+g" + rev, tree: tree{rev: "fedcba9876543210"}, wantErr: "another commit"},
		{name: "changed since a clean stamp", stamp: "1.2.3+g" + rev, tree: tree{rev: rev, dirty: true}, wantErr: "the tree has changes"},
		{name: "no nonce", stamp: "1.2.3+g" + rev + ".dirty.", tree: tree{rev: rev, dirty: true}, wantErr: "no nonce"},
		{name: "no build part", stamp: "1.2.3", tree: tree{rev: rev}, wantErr: "another commit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := matchTree(tt.stamp, tt.tree)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("matchTree() = %v, want a match", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("matchTree() = %v, want an error with %q", err, tt.wantErr)
			}
		})
	}
}

func TestGoBuildFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		stamp   string // "" writes no stamp file
		wantErr string
	}{
		{name: "no stamp", wantErr: "run mise run version:stamp"},
		{name: "stamp outside the form", stamp: "1.2.3+g01\n", wantErr: "not in the version form"},
		{name: "stamp for another commit", stamp: "1.2.3+g0000000000000000", wantErr: "another commit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t, "1.2.3\n")
			if tt.stamp != "" {
				if err := writeAtomic(filepath.Join(root, filepath.FromSlash(stampFile)), []byte(tt.stamp)); err != nil {
					t.Fatal(err)
				}
			}
			err := goBuild(context.Background(), root, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("goBuild() = %v, want an error with %q", err, tt.wantErr)
			}
		})
	}
}

func TestGoBuildRefusesATreeChangedSinceTheStamp(t *testing.T) {
	root := newRepo(t, "1.2.3\n")
	runStamp(t, root)
	write(t, filepath.Join(root, "new.go"), "package x\n")
	err := goBuild(context.Background(), root, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "the tree has changes") {
		t.Fatalf("goBuild() = %v, want a refusal of the changed tree", err)
	}
}

func TestCheck(t *testing.T) {
	root := newRepo(t, "1.2.3\n")
	v := runStamp(t, root)
	var out bytes.Buffer
	if err := check(context.Background(), root, &out); err != nil {
		t.Fatalf("check() right after the stamp = %v", err)
	}
	if !strings.Contains(out.String(), v) {
		t.Errorf("check() printed %q, want the version %s", out.String(), v)
	}

	write(t, filepath.Join(root, "VERSION"), "1.2.4\n")
	err := check(context.Background(), root, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "the tree has changes") {
		t.Fatalf("check() after a change = %v, want a refusal of the changed tree", err)
	}

	mustGit(t, root, "commit", "--quiet", "-am", "next")
	err = check(context.Background(), root, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "another commit") {
		t.Fatalf("check() after a commit = %v, want a refusal of the other commit", err)
	}
}

// TestLdflagsStampsTheProgram builds wb with the flag go-build passes and
// runs `wb --version`, so a wrong StampVar, which the linker ignores
// without an error, fails here instead of leaving every build unstamped.
func TestLdflagsStampsTheProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	const v = "1.2.3+g0123456789abcdef.dirty.00ff"
	// GOEXE is .exe on Windows, which runs no program without it.
	exe, err := exec.CommandContext(t.Context(), "go", "env", "GOEXE").Output()
	if err != nil {
		t.Fatalf("go env GOEXE: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "wb"+strings.TrimSpace(string(exe)))
	build := exec.CommandContext(t.Context(), "go", "build", ldflags(v), "-o", bin, "../../../cmd/wb")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(t.Context(), bin, "--version").Output()
	if err != nil {
		t.Fatalf("wb --version: %v", err)
	}
	if got, want := strings.TrimSpace(string(out)), "wb (Wraith Box) "+v; got != want {
		t.Errorf("wb --version = %q, want %q", got, want)
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"build"}, {"stamp", "extra"}} {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, &bytes.Buffer{}, &stderr); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("run(%q) printed %q, want the usage", args, stderr.String())
		}
	}
}
