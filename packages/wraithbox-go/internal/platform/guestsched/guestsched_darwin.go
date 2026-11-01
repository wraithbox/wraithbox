package guestsched

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Dir holds cron's and at's allow files, the crontabs and the at jobs.
// /usr/lib/cron, where crontab and at look, is a symbolic link to it.
// It is on the data volume, not the sealed system volume, so wb-guestd
// can write it.
const Dir = "/private/var/at"

// Lock writes CronAllow and AtAllow in Dir, each with Allowed only.
func Lock() error { return lockIn(Dir) }

// Clear removes name's crontab from Dir, and every at job whose file
// uid owns.
func (System) Clear(name string, uid uint32) (Cleared, error) { return clearIn(Dir, name, uid) }

// openDir opens path relative to parent as a directory, and refuses a
// symbolic link there.
func openDir(parent int, path string) (int, error) {
	return unix.Openat(parent, path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}

// lockIn writes both allow files in dir.
func lockIn(dir string) error {
	dfd, err := openDir(unix.AT_FDCWD, dir)
	if err != nil {
		return fmt.Errorf("guestsched: open %s: %w", dir, err)
	}
	defer func() { _ = unix.Close(dfd) }()
	for _, name := range []string{CronAllow, AtAllow} {
		if err := writeAllow(dfd, name); err != nil {
			return fmt.Errorf("guestsched: write %s/%s: %w", dir, name, err)
		}
	}
	if err := unix.Fsync(dfd); err != nil {
		return fmt.Errorf("guestsched: sync %s: %w", dir, err)
	}
	return nil
}

// writeAllow writes Allowed to a new file, created exclusively under a
// temporary name, made mode 0644 and synced, and renames it over name.
// So a symbolic link at either name is replaced, never followed.
func writeAllow(dfd int, name string) (err error) {
	tmp := ".wb-" + name + ".tmp"
	// A temporary file an earlier start left. A failure here shows at
	// the exclusive create below.
	_ = unix.Unlinkat(dfd, tmp, 0)
	fd, err := unix.Openat(dfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	f := os.NewFile(uintptr(fd), tmp)
	defer func() {
		if err != nil {
			_ = unix.Unlinkat(dfd, tmp, 0)
		}
	}()
	_, err = io.WriteString(f, Allowed)
	if err == nil {
		// The umask may have taken bits off the mode of the create.
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err = unix.Renameat(dfd, tmp, dfd, name); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// clearIn removes name's crontab from dir's Tabs, and uid's at jobs
// from its Jobs. A missing Tabs or Jobs directory holds nothing.
func clearIn(dir, name string, uid uint32) (Cleared, error) {
	var c Cleared
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return c, fmt.Errorf("guestsched: user name %q is not a file name", name)
	}
	dfd, err := openDir(unix.AT_FDCWD, dir)
	if err != nil {
		return c, fmt.Errorf("guestsched: open %s: %w", dir, err)
	}
	defer func() { _ = unix.Close(dfd) }()
	c.Tab, err = removeTab(dfd, name)
	if err != nil {
		return c, fmt.Errorf("guestsched: crontab of %s in %s: %w", name, dir, err)
	}
	c.Jobs, err = removeJobs(dfd, uid)
	if err != nil {
		return c, fmt.Errorf("guestsched: at jobs of uid %d in %s: %w", uid, dir, err)
	}
	return c, nil
}

// removeTab unlinks Tabs/name, and reports whether it was there.
func removeTab(dfd int, name string) (bool, error) {
	tfd, err := openDir(dfd, Tabs)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s: %w", Tabs, err)
	}
	defer func() { _ = unix.Close(tfd) }()
	err = unix.Unlinkat(tfd, name, 0)
	switch {
	case errors.Is(err, unix.ENOENT):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("unlink: %w", err)
	}
	return true, nil
}

// removeJobs unlinks each regular file in Jobs that uid owns, and
// returns how many it removed. It reads at most MaxJobEntries entries.
func removeJobs(dfd int, uid uint32) (int, error) {
	jfd, err := openDir(dfd, Jobs)
	if errors.Is(err, unix.ENOENT) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", Jobs, err)
	}
	f := os.NewFile(uintptr(jfd), Jobs)
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(MaxJobEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("read %s: %w", Jobs, err)
	}
	if len(names) > MaxJobEntries {
		return 0, fmt.Errorf("%s holds more than %d entries", Jobs, MaxJobEntries)
	}
	removed := 0
	for _, n := range names {
		var st unix.Stat_t
		err := unix.Fstatat(jfd, n, &st, unix.AT_SYMLINK_NOFOLLOW)
		switch {
		case errors.Is(err, unix.ENOENT):
			continue // atrun ran it meanwhile
		case err != nil:
			return removed, fmt.Errorf("stat %s/%s: %w", Jobs, n, err)
		case st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uid:
			continue
		}
		if err := unix.Unlinkat(jfd, n, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return removed, fmt.Errorf("unlink %s/%s: %w", Jobs, n, err)
		}
		removed++
	}
	return removed, nil
}
