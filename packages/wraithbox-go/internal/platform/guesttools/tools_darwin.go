package guesttools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// MountPoint is where Disk Arbitration mounts the tools disk's volume
	// at boot (X08-data-disk).
	MountPoint = "/Volumes/" + VolumeName
	// InstallDir is where wb-guestd installs the disk's programs. Its
	// LaunchDaemon runs wb-guestd from here.
	InstallDir = "/Library/WraithBox/libexec"
)

// OpenDisk opens the volume mounted at path, and checks that it can only
// be the tools disk wb-hostd attached: APFS, read-only, and mounted by
// root. Disk Arbitration mounts a disk attached at boot as root. A disk
// image a project user attaches is mounted as that user, and comes up at
// another path while the tools disk holds this one. The check is of the
// directory it opened, so another volume mounted at path afterwards isn't
// what the caller reads through the root it returns.
func OpenDisk(path string) (*os.Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, &Refusal{Rule: RuleNotMounted, Path: path, msg: err.Error()}
	}
	if err := checkRoot(path, root); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

// checkRoot checks the mount of root's own directory, with fstatfs.
func checkRoot(path string, root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return &Refusal{Rule: RuleNotMounted, Path: path, msg: err.Error()}
	}
	defer dir.Close()
	rc, err := dir.SyscallConn()
	if err != nil {
		return &Refusal{Rule: RuleNotMounted, Path: path, msg: err.Error()}
	}
	var st unix.Statfs_t
	var statErr error
	if err := rc.Control(func(fd uintptr) { statErr = unix.Fstatfs(int(fd), &st) }); err != nil {
		statErr = err
	}
	if statErr != nil {
		return &Refusal{Rule: RuleNotMounted, Path: path, msg: statErr.Error()}
	}
	return checkStatfs(path, &st)
}

func checkStatfs(path string, st *unix.Statfs_t) error {
	refuse := func(rule, format string, args ...any) error {
		return &Refusal{Rule: rule, Path: path, msg: fmt.Sprintf(format, args...)}
	}
	// os.OpenRoot follows a symbolic link at path, so this is the check
	// that refuses a link there to another volume.
	if on := unix.ByteSliceToString(st.Mntonname[:]); on != path {
		return refuse(RuleNotMounted, "no volume mounted here: the path is on %q", on)
	}
	if fstype := unix.ByteSliceToString(st.Fstypename[:]); fstype != "apfs" {
		return refuse(RuleUntrusted, "file system %q, not apfs", fstype)
	}
	if st.Flags&unix.MNT_RDONLY == 0 {
		return refuse(RuleUntrusted, "mounted read-write")
	}
	if st.Owner != 0 {
		return refuse(RuleUntrusted, "mounted by user ID %d, not root", st.Owner)
	}
	return nil
}

// PrepareDir makes dir and each missing parent of it, mode 0755, and
// checks dir and every parent: each is a directory and not a symbolic
// link, owned by root, and neither its group nor others can write it. So
// only root can change what wb-guestd installs there. dir is absolute.
func PrepareDir(dir string) error {
	return prepareDir(dir, 0)
}

func prepareDir(dir string, owner uint32) error {
	if !filepath.IsAbs(dir) {
		return &Refusal{Rule: RuleDirUnsafe, Path: dir, msg: "not an absolute path"}
	}
	for _, p := range pathAndParents(dir) {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(p, 0o755); err != nil { //nolint:gosec // G301: readable by all as /Library is; only root can write it, which checkDir checks
				return dirFailed(p, err)
			}
			// The umask may have taken bits off.
			if err := os.Chmod(p, 0o755); err != nil { //nolint:gosec // G302: a directory, readable by all as /Library is
				return dirFailed(p, err)
			}
		}
		if err := checkDir(p, owner); err != nil {
			return err
		}
	}
	return nil
}

// pathAndParents returns "/" and each parent of an absolute path, top
// down, and the path itself last.
func pathAndParents(dir string) []string {
	var paths []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		paths = append([]string{d}, paths...)
		if d == "/" {
			return paths
		}
	}
}

// checkDir checks one directory of PrepareDir, without following a link.
func checkDir(p string, owner uint32) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return dirFailed(p, err)
	}
	refuse := func(format string, args ...any) error {
		return &Refusal{Rule: RuleDirUnsafe, Path: p, msg: fmt.Sprintf(format, args...)}
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return refuse("a symbolic link")
	case !fi.IsDir():
		return refuse("not a directory")
	case !ok:
		return refuse("no owner")
	case st.Uid != owner:
		return refuse("owned by user ID %d, not %d", st.Uid, owner)
	case fi.Mode().Perm()&0o022 != 0:
		return refuse("mode %v: its group or others can write it", fi.Mode().Perm())
	}
	return nil
}

// dirFailed is the refusal of a directory PrepareDir couldn't make or
// look at.
func dirFailed(p string, err error) error {
	return &Refusal{Rule: RuleDirFailed, Path: p, msg: err.Error()}
}

// Exec runs the program at path in this process's place, with execve:
// the same process ID and environment, and the same arguments with path
// as the first. The service manager sees no exit, so its throttle
// doesn't hold the new program back. Exec returns only when execve
// fails.
func Exec(path string) error {
	argv := append([]string{path}, os.Args[1:]...)
	if err := unix.Exec(path, argv, os.Environ()); err != nil {
		return fmt.Errorf("guesttools: exec %s: %w", path, err)
	}
	return nil
}
