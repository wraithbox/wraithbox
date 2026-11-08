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

// The tests run checkAllowIn, findIn and scanIn against a temporary
// directory laid out as /private/var/at, never against the guest's own.
// The test user owns the files, so checkAllowIn takes its ID as the
// owner. The conformance check of a real guest is the attended step of
// I599.

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	writeMode(t, path, content, 0o600)
}

func writeMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// The umask may have taken bits off the mode.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// me is the test user's ID, the owner of the files the tests write.
func me() uint32 { return uint32(os.Getuid()) }

// allowDir returns a directory with both allow files as the build
// writes them.
func allowDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeMode(t, filepath.Join(dir, CronAllow), Allowed, 0o644)
	writeMode(t, filepath.Join(dir, AtAllow), Allowed, 0o644)
	writeFile(t, filepath.Join(dir, "cron.deny"), "Guest\n")
	return dir
}

// TestCheckAllow: allow files with root only, owned by the owner and
// writable by it alone, pass, and the check changes nothing.
func TestCheckAllow(t *testing.T) {
	dir := allowDir(t)
	for _, mode := range []os.FileMode{0o644, 0o600, 0o400} {
		if err := os.Chmod(filepath.Join(dir, AtAllow), mode); err != nil {
			t.Fatal(err)
		}
		if err := checkAllowIn(dir, me()); err != nil {
			t.Errorf("checkAllowIn with at.allow mode %#o = %v, want nil", mode, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Errorf("entries %v, %v, want the three files only", entries, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, CronAllow)); err != nil || string(b) != Allowed {
		t.Errorf("cron.allow = %q, %v, want it unchanged", b, err)
	}
}

// TestCheckAllowFailsClosed: every way an allow file can be wrong
// fails the check (S13-01-process-hardening, "Cron and at").
func TestCheckAllowFailsClosed(t *testing.T) {
	for name, c := range map[string]struct {
		file  string
		setup func(t *testing.T, path string)
		owner uint32
	}{
		"cron.allow missing": {CronAllow, func(t *testing.T, path string) { remove(t, path) }, me()},
		"at.allow missing":   {AtAllow, func(t *testing.T, path string) { remove(t, path) }, me()},
		"another user":       {CronAllow, func(t *testing.T, path string) { writeMode(t, path, "root\nwbp10000\n", 0o644) }, me()},
		"no newline":         {AtAllow, func(t *testing.T, path string) { writeMode(t, path, "root", 0o644) }, me()},
		"empty":              {AtAllow, func(t *testing.T, path string) { writeMode(t, path, "", 0o644) }, me()},
		"longer":             {CronAllow, func(t *testing.T, path string) { writeMode(t, path, "root\n\n", 0o644) }, me()},
		"group writable":     {CronAllow, func(t *testing.T, path string) { writeMode(t, path, Allowed, 0o664) }, me()},
		"others writable":    {AtAllow, func(t *testing.T, path string) { writeMode(t, path, Allowed, 0o646) }, me()},
		"another owner":      {CronAllow, func(*testing.T, string) {}, me() + 1},
		"directory": {AtAllow, func(t *testing.T, path string) {
			remove(t, path)
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}, me()},
		"fifo": {CronAllow, func(t *testing.T, path string) {
			remove(t, path)
			if err := unix.Mkfifo(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}, me()},
		"symbolic link": {CronAllow, func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "target")
			writeMode(t, target, Allowed, 0o644)
			remove(t, path)
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, me()},
	} {
		t.Run(name, func(t *testing.T) {
			dir := allowDir(t)
			c.setup(t, filepath.Join(dir, c.file))
			if err := checkAllowIn(dir, c.owner); err == nil {
				t.Errorf("checkAllowIn = nil, want an error")
			}
		})
	}
}

// TestCheckAllowDir: a directory that is missing or a symbolic link
// fails the check.
func TestCheckAllowDir(t *testing.T) {
	link := filepath.Join(t.TempDir(), "at")
	if err := os.Symlink(allowDir(t), link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "missing"), link} {
		if err := checkAllowIn(path, me()); err == nil {
			t.Errorf("checkAllowIn(%s) = nil, want an error", path)
		}
	}
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// TestFindTab: the user's crontab counts and stays, another user's
// doesn't count, and any entry at the name counts.
func TestFindTab(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, Tabs, "wbp10000"), "* * * * * /bin/sh -c x\n")
	writeFile(t, filepath.Join(dir, Tabs, "wbp10001"), "* * * * * /bin/sh -c y\n")
	f, err := findIn(dir, "wbp10000", 10000)
	if err != nil || f != (Found{Tabs: 1}) || !f.Any() {
		t.Fatalf("findIn = %+v, %v, want the tab", f, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, Tabs, "wbp10000")); err != nil {
		t.Errorf("tab: %v, want it left", err)
	}
	f, err = findIn(dir, "wbp10002", 10002)
	if err != nil || f.Any() {
		t.Errorf("findIn of a user without a tab = %+v, %v, want nothing", f, err)
	}
	if err := os.Symlink("wbp10001", filepath.Join(dir, Tabs, "wbp10003")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, Tabs, "wbp10004"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wbp10003", "wbp10004"} {
		if f, err := findIn(dir, name, 0); err != nil || f.Tabs != 1 {
			t.Errorf("findIn(%s) = %+v, %v, want the entry counted", name, f, err)
		}
	}
}

// TestFindJobs: each regular file in jobs that the user owns counts. A
// file of another owner, a directory and a symbolic link don't, and
// nothing is removed.
func TestFindJobs(t *testing.T) {
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
	f, err := findIn(dir, "wbp10000", me()+1)
	if err != nil || f.Any() {
		t.Fatalf("findIn of another uid = %+v, %v, want nothing", f, err)
	}
	f, err = findIn(dir, "wbp10000", me())
	if err != nil || f != (Found{Jobs: 2}) {
		t.Fatalf("findIn = %+v, %v, want 2 jobs", f, err)
	}
	entries, err := os.ReadDir(jobs)
	if err != nil || len(entries) != 4 {
		t.Errorf("jobs %v, %v, want all four entries left", entries, err)
	}
}

// TestFindNothing: a directory without tabs or jobs holds nothing.
func TestFindNothing(t *testing.T) {
	f, err := findIn(t.TempDir(), "wbp10000", 10000)
	if err != nil || f.Any() {
		t.Errorf("findIn = %+v, %v, want nothing and no error", f, err)
	}
}

// TestFindFailsClosed: a name that isn't a file name, a missing
// directory, a symbolic link at tabs or jobs and a jobs directory over
// the bound each fail the check.
func TestFindFailsClosed(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../wbp10000", "a/b", "a\x00b"} {
		if _, err := findIn(t.TempDir(), name, 10000); err == nil {
			t.Errorf("findIn(%q) = nil, want an error", name)
		}
	}
	if _, err := findIn(filepath.Join(t.TempDir(), "missing"), "wbp10000", 10000); err == nil {
		t.Error("findIn of a missing directory = nil, want an error")
	}
	for _, sub := range []string{Tabs, Jobs} {
		dir, target := linkedDir(t, sub)
		_, err := findIn(dir, "wbp10000", me())
		checkLinkRefused(t, err, target)
	}
	if _, err := findIn(fullDir(t, Jobs), "wbp10000", 10000); err == nil {
		t.Error("findIn over MaxEntries = nil, want an error")
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

// TestScanTabs: the start scan counts each tab with a project user's
// name, and removes none. A tab of another name doesn't count.
func TestScanTabs(t *testing.T) {
	dir := t.TempDir()
	tabs := filepath.Join(dir, Tabs)
	names := []string{"wbp10000", "wbp999999999", "root", "wbp9999", "wbp010000"}
	for _, name := range names {
		writeFile(t, filepath.Join(tabs, name), "* * * * * true\n")
	}
	f, err := scanIn(dir, projectuser.IsUserName, projectuser.InRange)
	if err != nil || f != (Found{Tabs: 2}) {
		t.Fatalf("scanIn = %+v, %v, want 2 tabs", f, err)
	}
	if entries, err := os.ReadDir(tabs); err != nil || len(entries) != len(names) {
		t.Errorf("tabs %v, %v, want all left", entries, err)
	}
	f, err = scanIn(t.TempDir(), projectuser.IsUserName, projectuser.InRange)
	if err != nil || f.Any() {
		t.Errorf("scanIn of an empty directory = %+v, %v, want nothing and no error", f, err)
	}
}

// TestScanJobs: the start scan counts each regular file in jobs whose
// owner it accepts. A job of another owner, a directory and a symbolic
// link don't count.
func TestScanJobs(t *testing.T) {
	dir := t.TempDir()
	jobs := filepath.Join(dir, Jobs)
	writeFile(t, filepath.Join(jobs, "a0000101a2b3c4"), "#!/bin/sh\n")
	if err := os.Mkdir(filepath.Join(jobs, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a0000101a2b3c4", filepath.Join(jobs, "link")); err != nil {
		t.Fatal(err)
	}
	f, err := scanIn(dir, projectuser.IsUserName, func(uid uint32) bool { return uid == me()+1 })
	if err != nil || f.Any() {
		t.Fatalf("scanIn of another owner = %+v, %v, want nothing", f, err)
	}
	f, err = scanIn(dir, projectuser.IsUserName, func(uid uint32) bool { return uid == me() })
	if err != nil || f != (Found{Jobs: 1}) {
		t.Fatalf("scanIn = %+v, %v, want 1 job", f, err)
	}
	if entries, err := os.ReadDir(jobs); err != nil || len(entries) != 3 {
		t.Errorf("jobs %v, %v, want all three entries left", entries, err)
	}
}

// TestScanFailsClosed: a missing directory, a symbolic link at tabs or
// jobs and a directory over the bound each fail the scan.
func TestScanFailsClosed(t *testing.T) {
	all := func(uint32) bool { return true }
	if _, err := scanIn(filepath.Join(t.TempDir(), "missing"), projectuser.IsUserName, all); err == nil {
		t.Error("scanIn of a missing directory = nil, want an error")
	}
	for _, sub := range []string{Tabs, Jobs} {
		dir, target := linkedDir(t, sub)
		_, err := scanIn(dir, projectuser.IsUserName, all)
		checkLinkRefused(t, err, target)
		if _, err := scanIn(fullDir(t, sub), projectuser.IsUserName, all); err == nil {
			t.Errorf("scanIn over MaxEntries in %s = nil, want an error", sub)
		}
	}
}
