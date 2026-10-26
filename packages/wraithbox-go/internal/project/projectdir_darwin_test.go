package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
)

const testID = "0123456789abcdef0123456789abcdef"

// wantRule fails the test unless err is a *Refusal with rule.
func wantRule(t *testing.T, err error, rule Rule) {
	t.Helper()
	r, ok := errors.AsType[*Refusal](err)
	if !ok || r.Rule != rule {
		t.Fatalf("err = %v, want rule %s", err, rule)
	}
}

// TestCheckID: only 32 lowercase hexadecimal characters are an id, so
// no path is built from an id wb-hostd sent with `..` or `/` in it.
func TestCheckID(t *testing.T) {
	for _, bad := range []string{"", "../../../../etc", testID[:31], testID + "0", strings.ToUpper(testID), testID[:31] + "g", "0123456789abcdef/123456789abcdef"} {
		wantRule(t, CheckID(bad), RuleIDForm)
		if IsID(bad) {
			t.Errorf("IsID(%q) = true", bad)
		}
		if _, err := Dir("/data", bad); err == nil {
			t.Errorf("Dir(%q): want a refusal", bad)
		}
	}
	if err := CheckID(testID); err != nil {
		t.Errorf("CheckID: %v", err)
	}
	if d, err := Dir("/data", testID); err != nil || d != "/data/projects/"+testID {
		t.Errorf("Dir = %q, %v", d, err)
	}
}

// TestEnsureDir: the project directory is made with mode 0700 when it
// is missing, at registration or later, and an entry that isn't a
// directory of the user's own is refused (SEC12-least-privilege).
func TestEnsureDir(t *testing.T) {
	data := tempDir(t)
	_, err := EnsureDir(data, testID)
	wantRule(t, err, RuleProjectDir)
	if !strings.Contains(err.Error(), "run wb setup") {
		t.Errorf("no projects root: %v", err)
	}

	mkdir(t, Root(data))
	dir, err := EnsureDir(data, testID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("project directory %v, %v: want a directory of mode 0700", info, err)
	}
	if _, err := EnsureDir(data, testID); err != nil {
		t.Errorf("an existing directory: %v", err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDir(data, testID); err != nil {
		t.Errorf("a directory gone since registration is made again: %v", err)
	}

	other := "fedcba9876543210fedcba9876543210"
	symlink(t, tempDir(t), filepath.Join(Root(data), other))
	_, err = EnsureDir(data, other)
	wantRule(t, err, RuleProjectDir)

	file := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writeFile(t, filepath.Join(Root(data), file), "")
	_, err = EnsureDir(data, file)
	wantRule(t, err, RuleProjectDir)

	_, err = EnsureDir(data, "../x")
	wantRule(t, err, RuleIDForm)
}

// TestRemoveFiles is the file half of C04-projects, "Project commands",
// for rm: the project directory and both settings files go, and a
// symbolic link inside landing.git that names a file outside leaves
// that file in place.
func TestRemoveFiles(t *testing.T) {
	root := tempDir(t)
	p := paths.Paths{Data: filepath.Join(root, "data"), Config: filepath.Join(root, "config"), Logs: filepath.Join(root, "logs")}
	mkdir(t, Root(p.Data))
	dir, err := EnsureDir(p.Data, testID)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	writeFile(t, outside, "keep")
	writeFile(t, filepath.Join(dir, "landing.git", "HEAD"), "ref: refs/heads/main\n")
	symlink(t, outside, filepath.Join(dir, "landing.git", "planted"))
	symlink(t, root, filepath.Join(dir, "landing.git", "planted-dir"))
	settings := filepath.Join(p.Config, "projects", testID+".toml")
	policy := filepath.Join(p.Config, "projects", testID+".policy.yaml")
	writeFile(t, settings, "")
	writeFile(t, policy, "")

	if err := RemoveFiles(p, testID); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{dir, settings, policy} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
		t.Errorf("the file outside: %q, %v", b, err)
	}

	// Nothing left is no error. An id that isn't one removes nothing.
	if err := RemoveFiles(p, testID); err != nil {
		t.Errorf("a second rm: %v", err)
	}
	wantRule(t, RemoveFiles(p, ".."), RuleIDForm)
	if _, err := os.Stat(p.Data); err != nil {
		t.Errorf("<data>: %v", err)
	}
}

// TestRemoveFilesLeft: a file that can't be removed is named in a
// refusal.
func TestRemoveFilesLeft(t *testing.T) {
	root := tempDir(t)
	p := paths.Paths{Data: filepath.Join(root, "data"), Config: filepath.Join(root, "config"), Logs: filepath.Join(root, "logs")}
	settings := filepath.Join(p.Config, "projects", testID+".toml")
	mkdir(t, filepath.Join(settings, "sub"))
	err := RemoveFiles(p, testID)
	wantRule(t, err, RuleFilesLeft)
	if !strings.Contains(err.Error(), settings) {
		t.Errorf("message %q doesn't name %s", err, settings)
	}
}

// TestMovedMatch: a match whose location is gone, isn't a directory,
// holds no HEAD, is in another realm, or isn't an absolute path counts
// as moved, without running git. A match in place doesn't.
func TestMovedMatch(t *testing.T) {
	root := tempDir(t)
	inPlace := filepath.Join(newRepo(t, filepath.Join(root, "a")), ".git")
	noHead := mkdir(t, filepath.Join(root, "no-head"))
	file := filepath.Join(root, "file")
	writeFile(t, file, "")
	match := func(realm, path string) *hostdv1.ProjectMatch {
		return &hostdv1.ProjectMatch{Id: testID, Name: "a", Key: &hostdv1.ProjectKey{Realm: realm, Path: path}}
	}
	if ref := movedMatch(root, []*hostdv1.ProjectMatch{match("native", inPlace)}); ref != nil {
		t.Errorf("a match in place: %v", ref)
	}
	for _, m := range []*hostdv1.ProjectMatch{
		match("native", filepath.Join(root, "gone")),
		match("native", file),
		match("native", noHead),
		match("wsl:Ubuntu", inPlace),
		match("native", "relative/.git"),
		match("native", inPlace+"\x1b[2J"),
	} {
		ref := movedMatch("/home/u/it's here", []*hostdv1.ProjectMatch{match("native", inPlace), m})
		if ref == nil || ref.Rule != RuleMatchMoved {
			t.Errorf("match %v: %v, want %s", m.GetKey(), ref, RuleMatchMoved)
			continue
		}
		for _, want := range []string{"wb project move '/home/u/it'\\''s here' --project " + testID, "wb project rm --project " + testID} {
			if !strings.Contains(ref.Error(), want) {
				t.Errorf("message %q misses %q", ref, want)
			}
		}
	}
}

// TestNotice names the new project and each match with its placement.
func TestNotice(t *testing.T) {
	op := &Opened{
		Local:      &Local{Repo: &Repo{Dir: "/r/b"}},
		Project:    &hostdv1.Project{Id: testID, Name: "b", Placement: hostdv1.Placement_PLACEMENT_ISOLATED},
		Registered: true,
		Matches:    []*hostdv1.ProjectMatch{{Id: "f" + testID[1:], Name: "a", Key: &hostdv1.ProjectKey{Path: "/r/a/.git"}, Placement: hostdv1.Placement_PLACEMENT_ISOLATED}},
		MatchCount: 3,
	}
	got := strings.Join(op.Notice(), "\n")
	for _, want := range []string{"registered project b (" + testID + ") for /r/b, placement isolated", "project a (f" + testID[1:] + ") at /r/a/.git, placement isolated", "and 2 more"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q misses %q", got, want)
		}
	}
	op.Registered = false
	if n := op.Notice(); n != nil {
		t.Errorf("notice of a found project: %q", n)
	}
}
