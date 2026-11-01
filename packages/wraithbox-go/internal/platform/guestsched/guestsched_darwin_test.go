package guestsched

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// The tests run lockIn, clearIn and sweepIn against a temporary directory laid
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
// directory, a symbolic link at tabs or jobs, a tab that can't be
// unlinked and a jobs directory over the bound each fail the clear.
func TestClearFailsClosed(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../wbp10000", "a/b", "a\x00b"} {
		if _, err := clearIn(t.TempDir(), name, 10000); err == nil {
			t.Errorf("clearIn(%q) = nil, want an error", name)
		}
	}
	if _, err := clearIn(filepath.Join(t.TempDir(), "missing"), "wbp10000", 10000); err == nil {
		t.Error("clearIn of a missing directory = nil, want an error")
	}

	for _, sub := range []string{Tabs, Jobs} {
		dir, target := linkedDir(t, sub)
		_, err := clearIn(dir, "wbp10000", uint32(os.Getuid()))
		checkLinkRefused(t, err, target)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Tabs, "wbp10000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := clearIn(dir, "wbp10000", 10000); err == nil {
		t.Error("clearIn with a directory at the tab = nil, want an error")
	}

	if _, err := clearIn(fullDir(t, Jobs), "wbp10000", 10000); err == nil {
		t.Error("clearIn over MaxEntries = nil, want an error")
	}
}

// linkedDir returns a directory laid out as Dir whose sub is a symbolic
// link to another directory, and the path of a file in that one. The
// file is named as a project user's tab, and the test's user owns it.
func linkedDir(t *testing.T, sub string) (dir, target string) {
	t.Helper()
	dir, elsewhere := t.TempDir(), t.TempDir()
	target = filepath.Join(elsewhere, "wbp10000")
	writeFile(t, target, "* * * * * true\n")
	if err := os.Symlink(elsewhere, filepath.Join(dir, sub)); err != nil {
		t.Fatal(err)
	}
	return dir, target
}

// checkLinkRefused checks that err is the open of a symbolic link
// refused, and that target, past the link, is still there. With
// O_DIRECTORY and O_NOFOLLOW, darwin refuses the link with ENOTDIR,
// and other systems with ELOOP.
func checkLinkRefused(t *testing.T, err error, target string) {
	t.Helper()
	if !errors.Is(err, unix.ELOOP) && !errors.Is(err, unix.ENOTDIR) {
		t.Errorf("err = %v, want ELOOP or ENOTDIR", err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Errorf("the file past the link: %v", err)
	}
}

// fullDir returns a directory laid out as Dir whose sub holds one entry
// more than MaxEntries.
func fullDir(t *testing.T, sub string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, sub)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range MaxEntries + 1 {
		f, err := os.Create(filepath.Join(path, "wbp"+strconv.Itoa(10000+i)))
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	return dir
}

// TestSweepTabs: the start sweep removes each tab with a project user's
// name. A tab of another name stays.
func TestSweepTabs(t *testing.T) {
	dir := t.TempDir()
	tabs := filepath.Join(dir, Tabs)
	keep := map[string]bool{"wbp10000": false, "wbp999999999": false, "root": true, "wbp9999": true, "wbp010000": true}
	for name := range keep {
		writeFile(t, filepath.Join(tabs, name), "* * * * * true\n")
	}
	s, err := sweepIn(dir, projectuser.IsUserName, projectuser.InRange)
	if err != nil || s.Tabs != 2 || s.Jobs != 0 {
		t.Fatalf("sweepIn = %+v, %v, want 2 tabs removed", s, err)
	}
	for name, want := range keep {
		if _, err := os.Lstat(filepath.Join(tabs, name)); (err == nil) != want {
			t.Errorf("tab %s: %v, want kept %v", name, err, want)
		}
	}
	s, err = sweepIn(t.TempDir(), projectuser.IsUserName, projectuser.InRange)
	if err != nil || s != (Swept{}) {
		t.Errorf("sweepIn of an empty directory = %+v, %v, want nothing and no error", s, err)
	}
}

// TestSweepJobs: the start sweep removes each regular file in jobs whose
// owner it accepts. A job of another owner, a directory and a symbolic
// link stay.
func TestSweepJobs(t *testing.T) {
	dir := t.TempDir()
	jobs := filepath.Join(dir, Jobs)
	writeFile(t, filepath.Join(jobs, "a0000101a2b3c4"), "#!/bin/sh\n")
	if err := os.Mkdir(filepath.Join(jobs, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a0000101a2b3c4", filepath.Join(jobs, "link")); err != nil {
		t.Fatal(err)
	}
	me := uint32(os.Getuid())
	other := func(uid uint32) bool { return uid == me+1 }
	s, err := sweepIn(dir, projectuser.IsUserName, other)
	if err != nil || s != (Swept{}) {
		t.Fatalf("sweepIn of another owner = %+v, %v, want nothing removed", s, err)
	}
	s, err = sweepIn(dir, projectuser.IsUserName, func(uid uint32) bool { return uid == me })
	if err != nil || s.Tabs != 0 || s.Jobs != 1 {
		t.Fatalf("sweepIn = %+v, %v, want 1 job removed", s, err)
	}
	entries, err := os.ReadDir(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("jobs left %v, want sub and link", entries)
	}
}

// TestSweepFailsClosed: a missing directory, a symbolic link at tabs or
// jobs, a tab that can't be unlinked and a directory over the bound each
// fail the sweep.
func TestSweepFailsClosed(t *testing.T) {
	all := func(uint32) bool { return true }
	if _, err := sweepIn(filepath.Join(t.TempDir(), "missing"), projectuser.IsUserName, all); err == nil {
		t.Error("sweepIn of a missing directory = nil, want an error")
	}
	for _, sub := range []string{Tabs, Jobs} {
		dir, target := linkedDir(t, sub)
		_, err := sweepIn(dir, projectuser.IsUserName, all)
		checkLinkRefused(t, err, target)
		if _, err := sweepIn(fullDir(t, sub), projectuser.IsUserName, all); err == nil {
			t.Errorf("sweepIn over MaxEntries in %s = nil, want an error", sub)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Tabs, "wbp10000", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := sweepIn(dir, projectuser.IsUserName, all); err == nil {
		t.Error("sweepIn with a directory at a tab = nil, want an error")
	}
}
