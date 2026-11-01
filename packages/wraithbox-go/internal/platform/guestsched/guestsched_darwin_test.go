package guestsched

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The tests run lockIn and clearIn against a temporary directory laid
// out as /private/var/at, never against the guest's own. The
// conformance check of a real guest is the attended step of I442.

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkAllow(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o644 {
		t.Errorf("%s: mode %v, want a regular file with mode 0644", name, fi.Mode())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != Allowed {
		t.Errorf("%s = %q, want %q", name, b, Allowed)
	}
}

// TestLock: both allow files hold root only and have mode 0644, an
// earlier list and an earlier start's temporary file are replaced, and
// no temporary file is left.
func TestLock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, CronAllow), "root\n"+"wbp10000\n")
	writeFile(t, filepath.Join(dir, ".wb-"+AtAllow+".tmp"), "wbp10000\n")
	writeFile(t, filepath.Join(dir, "cron.deny"), "Guest\n")
	if err := lockIn(dir); err != nil {
		t.Fatal(err)
	}
	checkAllow(t, dir, CronAllow)
	checkAllow(t, dir, AtAllow)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("entries %v, want cron.allow, at.allow and cron.deny only", entries)
	}
	// A second start writes the same.
	if err := lockIn(dir); err != nil {
		t.Fatal(err)
	}
	checkAllow(t, dir, CronAllow)
}

// TestLockReplacesLink: a symbolic link at an allow file is replaced by
// the file, and its target is left alone.
func TestLockReplacesLink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "target")
	writeFile(t, target, "kept\n")
	if err := os.Symlink(target, filepath.Join(dir, CronAllow)); err != nil {
		t.Fatal(err)
	}
	if err := lockIn(dir); err != nil {
		t.Fatal(err)
	}
	checkAllow(t, dir, CronAllow)
	if b, err := os.ReadFile(target); err != nil || string(b) != "kept\n" {
		t.Errorf("link target = %q, %v, want it unchanged", b, err)
	}
}

// TestLockFailsClosed: a directory that is missing or a symbolic link
// fails the lock, and nothing is written through the link.
func TestLockFailsClosed(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "at")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing"), link} {
		if err := lockIn(path); err == nil {
			t.Errorf("lockIn(%s) = nil, want an error", path)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %v through the link", entries)
	}
}

// TestClearTab: the user's crontab goes, another user's stays, and a
// second clear finds none.
func TestClearTab(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, Tabs, "wbp10000"), "* * * * * /bin/sh -c x\n")
	writeFile(t, filepath.Join(dir, Tabs, "wbp10001"), "* * * * * /bin/sh -c y\n")
	c, err := clearIn(dir, "wbp10000", 10000)
	if err != nil || !c.Tab || c.Jobs != 0 || !c.Any() {
		t.Fatalf("clearIn = %+v, %v, want the tab removed", c, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, Tabs, "wbp10000")); !os.IsNotExist(err) {
		t.Errorf("tab still there: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, Tabs, "wbp10001")); err != nil {
		t.Errorf("another user's tab: %v", err)
	}
	c, err = clearIn(dir, "wbp10000", 10000)
	if err != nil || c.Any() {
		t.Errorf("second clearIn = %+v, %v, want nothing removed", c, err)
	}
}

// TestClearJobs: each regular file in jobs that the user owns goes. A
// file of another owner, a directory and a symbolic link stay.
func TestClearJobs(t *testing.T) {
	dir := t.TempDir()
	jobs := filepath.Join(dir, Jobs)
	writeFile(t, filepath.Join(jobs, "a0000101a2b3c4"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(jobs, "a0000201a2b3c5"), "#!/bin/sh\n")
	if err := os.Mkdir(filepath.Join(jobs, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a0000101a2b3c4", filepath.Join(jobs, "link")); err != nil {
		t.Fatal(err)
	}
	me := uint32(os.Getuid())
	c, err := clearIn(dir, "wbp10000", me+1)
	if err != nil || c.Any() {
		t.Fatalf("clearIn of another uid = %+v, %v, want nothing removed", c, err)
	}
	c, err = clearIn(dir, "wbp10000", me)
	if err != nil || c.Jobs != 2 || c.Tab {
		t.Fatalf("clearIn = %+v, %v, want 2 jobs removed", c, err)
	}
	entries, err := os.ReadDir(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("jobs left %v, want sub and link", entries)
	}
}

// TestClearNothing: a directory without tabs or jobs holds nothing to
// clear.
func TestClearNothing(t *testing.T) {
	c, err := clearIn(t.TempDir(), "wbp10000", 10000)
	if err != nil || c.Any() {
		t.Errorf("clearIn = %+v, %v, want nothing and no error", c, err)
	}
}

// TestClearFailsClosed: a name that isn't a file name, a missing
// directory, a tab that can't be unlinked and a jobs directory over the
// bound each fail the clear.
func TestClearFailsClosed(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../wbp10000", "a/b", "a\x00b"} {
		if _, err := clearIn(t.TempDir(), name, 10000); err == nil {
			t.Errorf("clearIn(%q) = nil, want an error", name)
		}
	}
	if _, err := clearIn(filepath.Join(t.TempDir(), "missing"), "wbp10000", 10000); err == nil {
		t.Error("clearIn of a missing directory = nil, want an error")
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Tabs, "wbp10000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := clearIn(dir, "wbp10000", 10000); err == nil {
		t.Error("clearIn with a directory at the tab = nil, want an error")
	}

	dir = t.TempDir()
	jobs := filepath.Join(dir, Jobs)
	if err := os.Mkdir(jobs, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range MaxJobEntries + 1 {
		f, err := os.Create(filepath.Join(jobs, strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	if _, err := clearIn(dir, "wbp10000", 10000); err == nil {
		t.Error("clearIn over MaxJobEntries = nil, want an error")
	}
}
