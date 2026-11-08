package guestsched

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// Dir holds cron's and at's allow files, the crontabs and the at jobs.
// /usr/lib/cron, where crontab and at look, is a symbolic link to it.
// TCC protects it: only a process with Full Disk Access, such as a
// Remote Login session, can create or remove a file in it.
const Dir = "/private/var/at"

// CheckAllow checks CronAllow and AtAllow in Dir: each is a regular
// file owned by root, not writable by group or others, that holds
// Allowed and nothing else (S13-01-process-hardening, "Cron and at").
func CheckAllow() error { return checkAllowIn(Dir, 0) }

// Find counts name's crontab in Dir, and every at job whose file uid
// owns.
func (System) Find(name string, uid uint32) (Found, error) { return findIn(Dir, name, uid) }

// Scan counts in Dir the crontab of every project user, and every at
// job whose file a project user's ID owns.
func Scan() (Found, error) {
	return scanIn(Dir, projectuser.IsUserName, projectuser.InRange)
}

// openDir opens path relative to parent as a directory, and refuses a
// symbolic link there.
func openDir(parent int, path string) (int, error) {
	return unix.Openat(parent, path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}

// checkAllowIn checks both allow files in dir, each owned by owner.
func checkAllowIn(dir string, owner uint32) error {
	dfd, err := openDir(unix.AT_FDCWD, dir)
	if err != nil {
		return fmt.Errorf("guestsched: open %s: %w", dir, err)
	}
	defer func() { _ = unix.Close(dfd) }()
	for _, name := range []string{CronAllow, AtAllow} {
		if err := checkAllow(dfd, name, owner); err != nil {
			return fmt.Errorf("guestsched: %s/%s: %w", dir, name, err)
		}
	}
	return nil
}

// checkAllow opens name without following a symbolic link, and checks
// that it is a regular file owned by owner, not writable by group or
// others, whose content is Allowed. O_NONBLOCK keeps the open of a FIFO
// from waiting for a writer. The type check then refuses it.
func checkAllow(dfd int, name string, owner uint32) error {
	fd, err := unix.Openat(dfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return fmt.Errorf("not a regular file (mode %#o)", st.Mode)
	case st.Uid != owner:
		return fmt.Errorf("owned by uid %d, want %d", st.Uid, owner)
	case st.Mode&0o022 != 0:
		return fmt.Errorf("mode %#o is writable by group or others", st.Mode&0o7777)
	}
	// One byte more than Allowed shows a longer file.
	b, err := io.ReadAll(io.LimitReader(f, int64(len(Allowed))+1))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if string(b) != Allowed {
		return fmt.Errorf("content %q, want %q", b, Allowed)
	}
	return nil
}

// findIn counts name's crontab in dir's Tabs, and uid's at jobs in its
// Jobs. A missing Tabs or Jobs directory holds nothing.
func findIn(dir, name string, uid uint32) (Found, error) {
	var f Found
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return f, fmt.Errorf("guestsched: user name %q is not a file name", name)
	}
	dfd, err := openDir(unix.AT_FDCWD, dir)
	if err != nil {
		return f, fmt.Errorf("guestsched: open %s: %w", dir, err)
	}
	defer func() { _ = unix.Close(dfd) }()
	f.Tabs, err = findTab(dfd, name)
	if err != nil {
		return f, fmt.Errorf("guestsched: crontab of %s in %s: %w", name, dir, err)
	}
	f.Jobs, err = countJobs(dfd, func(owner uint32) bool { return owner == uid })
	if err != nil {
		return f, fmt.Errorf("guestsched: at jobs of uid %d in %s: %w", uid, dir, err)
	}
	return f, nil
}

// findTab returns 1 when Tabs holds an entry named name, of any type,
// and 0 when it doesn't.
func findTab(dfd int, name string) (int, error) {
	tfd, err := openDir(dfd, Tabs)
	if errors.Is(err, unix.ENOENT) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", Tabs, err)
	}
	defer func() { _ = unix.Close(tfd) }()
	var st unix.Stat_t
	err = unix.Fstatat(tfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("stat: %w", err)
	}
	return 1, nil
}

// scanIn counts, in dir, each crontab whose name user accepts, and each
// at job whose owner's ID owner accepts. A missing Tabs or Jobs
// directory holds nothing.
func scanIn(dir string, user func(name string) bool, owner func(uid uint32) bool) (Found, error) {
	var f Found
	dfd, err := openDir(unix.AT_FDCWD, dir)
	if err != nil {
		return f, fmt.Errorf("guestsched: open %s: %w", dir, err)
	}
	defer func() { _ = unix.Close(dfd) }()
	f.Tabs, err = countTabs(dfd, user)
	if err != nil {
		return f, fmt.Errorf("guestsched: crontabs of project users in %s: %w", dir, err)
	}
	f.Jobs, err = countJobs(dfd, owner)
	if err != nil {
		return f, fmt.Errorf("guestsched: at jobs of project users in %s: %w", dir, err)
	}
	return f, nil
}

// listDir opens sub in dfd as a directory, without following a
// symbolic link, and reads at most MaxEntries names from it. A missing
// sub gives no file, no names and no error. The caller closes the file.
func listDir(dfd int, sub string) (*os.File, []string, error) {
	fd, err := openDir(dfd, sub)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", sub, err)
	}
	f := os.NewFile(uintptr(fd), sub)
	names, err := f.Readdirnames(MaxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("read %s: %w", sub, err)
	}
	if len(names) > MaxEntries {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s holds more than %d entries", sub, MaxEntries)
	}
	return f, names, nil
}

// countTabs counts the entries of Tabs whose name user accepts, of any
// type. It reads at most MaxEntries entries.
func countTabs(dfd int, user func(name string) bool) (int, error) {
	f, names, err := listDir(dfd, Tabs)
	if f == nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	n := 0
	for _, name := range names {
		if user(name) {
			n++
		}
	}
	return n, nil
}

// countJobs counts the regular files in Jobs whose owner's ID owner
// accepts. It reads at most MaxEntries entries. at writes each job as a regular
// file, so it skips an entry of any other type.
func countJobs(dfd int, owner func(uid uint32) bool) (int, error) {
	f, names, err := listDir(dfd, Jobs)
	if f == nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	jfd := int(f.Fd())
	n := 0
	for _, name := range names {
		var st unix.Stat_t
		err := unix.Fstatat(jfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
		switch {
		case errors.Is(err, unix.ENOENT):
			continue // atrun ran it meanwhile
		case err != nil:
			return n, fmt.Errorf("stat %s/%s: %w", Jobs, name, err)
		case st.Mode&unix.S_IFMT == unix.S_IFREG && owner(st.Uid):
			n++
		}
	}
	return n, nil
}
