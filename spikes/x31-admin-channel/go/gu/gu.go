// Copied from packages/wraithbox-go/internal (train/604) for the X31-admin-channel
// spike, so the spike's two daemons run the product's update steps. Throwaway.
package gu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func label(s string) string { return fmt.Sprintf("%q", s) }

func validVersion(s string) bool { return s != "" && len(s) <= 64 && !bytes.ContainsAny([]byte(s), "\n\r ") }
// Package guesttools is the guest's end of the guest tools disk: where
// the disk is mounted, the checks that the mount is the disk wb-hostd
// attached, where its programs are installed, and how wb-guestd runs the
// installed wb-guestd in its own place (S06-12-guest-agent, "Update at
// start"). internal/guestupdate runs the update with it.
//
// It is in internal/platform because each of these is per OS
// (S12-01-platform-interfaces, "Rules"). Linux and Windows guests are
// later, and there every call returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").

// VolumeName is the name of the tools disk's volume
// (scripts/app-bundle.sh, step 5).
const VolumeName = "wb-guest-tools"

// The rules of this package, for the log.
const (
	// RuleNotMounted: no volume is mounted at the mount point.
	RuleNotMounted = "guestd-tools-not-mounted"
	// RuleUntrusted: the volume at the mount point isn't the disk wb-hostd
	// attached: it is writable, a user mounted it, or it isn't APFS.
	RuleUntrusted = "guestd-tools-untrusted"
	// RuleDirUnsafe: the install directory or a parent of it is a
	// symbolic link or not a directory, isn't owned by root, or its group
	// or others can write it.
	RuleDirUnsafe = "guestd-install-dir-unsafe"
	// RuleDirFailed: the install directory or a parent of it couldn't be
	// made or looked at, for a reason other than what it is, such as an
	// I/O error.
	RuleDirFailed = "guestd-install-dir-failed"
)

// ToolsRefusal is the error of a refusal in this package.
type ToolsRefusal struct {
	Rule string
	Path string
	msg  string
}

func (r *ToolsRefusal) Error() string { return "guesttools: " + r.Path + ": " + r.msg + " (" + r.Rule + ")" }


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
		return nil, &ToolsRefusal{Rule: RuleNotMounted, Path: path, msg: err.Error()}
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
		return &ToolsRefusal{Rule: RuleNotMounted, Path: path, msg: err.Error()}
	}
	defer dir.Close()
	rc, err := dir.SyscallConn()
	if err != nil {
		return &ToolsRefusal{Rule: RuleNotMounted, Path: path, msg: err.Error()}
	}
	var st unix.Statfs_t
	var statErr error
	if err := rc.Control(func(fd uintptr) { statErr = unix.Fstatfs(int(fd), &st) }); err != nil {
		statErr = err
	}
	if statErr != nil {
		return &ToolsRefusal{Rule: RuleNotMounted, Path: path, msg: statErr.Error()}
	}
	return checkStatfs(path, &st)
}

func checkStatfs(path string, st *unix.Statfs_t) error {
	refuse := func(rule, format string, args ...any) error {
		return &ToolsRefusal{Rule: rule, Path: path, msg: fmt.Sprintf(format, args...)}
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
		return &ToolsRefusal{Rule: RuleDirUnsafe, Path: dir, msg: "not an absolute path"}
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
		return &ToolsRefusal{Rule: RuleDirUnsafe, Path: p, msg: fmt.Sprintf(format, args...)}
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
	return &ToolsRefusal{Rule: RuleDirFailed, Path: p, msg: err.Error()}
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
// Package guestupdate is wb-guestd's update at start from the guest
// tools disk (S06-12-guest-agent, "Update at start"). Before wb-guestd
// listens for the host, AtStart waits for the disk and compares
// wb-guestd's own build version with the disk's VERSION. When they
// differ, it installs the disk's programs and runs the installed
// wb-guestd in its place.
//
// A refusal never stops wb-guestd. It is logged with its rule, and
// wb-guestd serves as it is: wb-hostd accepts only a wb-guestd of its
// own build version, so a guest that didn't update gets no session
// (S06-12-guest-agent, "The host checks, not the guest").


const (
	// DiskWait is how long AtStart waits for the tools disk. Disk
	// Arbitration mounts it during boot, which may be after launchd has
	// started wb-guestd.
	DiskWait = 30 * time.Second
	// pollEvery is how often AtStart looks for the disk while it waits.
	pollEvery = 100 * time.Millisecond
	// MaxProgramSize caps each program AtStart copies from the disk.
	MaxProgramSize = 256 << 20
)

// The rules of this package, for the log. The rules of
// internal/platform/guesttools name a disk that isn't there or isn't the
// one wb-hostd attached, and an unsafe install directory.
const (
	// RuleVersionInvalid: the disk's VERSION is missing, isn't a regular
	// file, or doesn't hold a build version of at most 64
	// bytes.
	RuleVersionInvalid = "guestd-update-version-invalid"
	// RuleProgramInvalid: a program of the layout is missing from the
	// disk, isn't a regular file, or is larger than MaxProgramSize.
	// Nothing is installed.
	RuleProgramInvalid = "guestd-update-program-invalid"
	// RuleNoEffect: the installed wb-guestd is already the disk's, yet
	// the disk's VERSION isn't wb-guestd's own version. Installing again
	// would loop, so wb-guestd serves as it is.
	RuleNoEffect = "guestd-update-no-effect"
	// RuleInstallFailed: opening the install directory, or copying a
	// program into it, failed. The programs installed before it stay, and the next start
	// tries again, because wb-guestd is installed last.
	RuleInstallFailed = "guestd-update-install-failed"
	// RuleExecFailed: running the installed wb-guestd failed. wb-guestd
	// exits, and the service manager starts the installed one.
	RuleExecFailed = "guestd-update-exec-failed"
	// RuleUnsupported: the guest OS has no tools disk yet
	// (internal/platform/guesttools).
	RuleUnsupported = "guestd-update-unsupported"
)

// Refusal is the error of a refusal in this package.
type Refusal struct {
	Rule string
	err  error
}

func (r *Refusal) Error() string { return "guestupdate: " + r.err.Error() + " (" + r.Rule + ")" }

// Unwrap returns the cause.
func (r *Refusal) Unwrap() error { return r.err }

// Platform is what the update needs from the guest OS:
// internal/platform/guesttools, or a fake in tests.
type Platform interface {
	// OpenDisk opens the tools disk mounted at path, and checks the mount
	// of the directory it opened.
	OpenDisk(path string) (*os.Root, error)
	// PrepareDir makes the install directory when it is missing, and
	// checks that only root can change it.
	PrepareDir(dir string) error
	// Exec runs the program at path in this process's place. It
	// returns only when that fails.
	Exec(path string) error
}

type system struct{}

func (system) OpenDisk(path string) (*os.Root, error) { return OpenDisk(path) }
func (system) PrepareDir(dir string) error            { return PrepareDir(dir) }
func (system) Exec(path string) error                 { return Exec(path) }

// Config is what AtStart runs with. Only Own and Log are set outside
// tests.
type Config struct {
	// Own is wb-guestd's own build version, normally version.Build().
	Own string
	Log *slog.Logger
	// Disk is the tools disk's mount point. "" takes
	// MountPoint.
	Disk string
	// Dir is the install directory. "" takes InstallDir.
	Dir string
	// Programs are the names of the programs to install. nil takes
	// Programs().
	Programs []string
	// Platform nil takes internal/platform/
	Platform Platform
	// Wait is how long to wait for the disk. 0 takes DiskWait.
	Wait time.Duration
	// MaxSize caps each program. 0 takes MaxProgramSize.
	MaxSize int64
}

func (c Config) withDefaults() Config {
	if c.Disk == "" {
		c.Disk = MountPoint
	}
	if c.Dir == "" {
		c.Dir = InstallDir
	}
	if c.Programs == nil {
		c.Programs = Programs()
	}
	if c.Platform == nil {
		c.Platform = system{}
	}
	if c.Wait == 0 {
		c.Wait = DiskWait
	}
	if c.MaxSize == 0 {
		c.MaxSize = MaxProgramSize
	}
	return c
}

// Programs returns the names of the programs on the tools disk, from
// the layout table of internal/appbundle, with wb-guestd last.
func Programs() []string { return []string{SelfName} }

// SelfName is the installing program's name on the disk and in the
// install directory. The spike sets it: wb-guestroot installs only itself.
var SelfName = "wb-guestd"

// AtStart is the update at start. It returns nil when wb-guestd is to
// serve as it is: the disk's version is its own, or the update was
// refused, and the refusal logged with its rule. After an install it
// runs the installed wb-guestd in this process's place, and returns only
// when that fails: wb-guestd then exits non-zero, and the service manager
// starts the installed one. It returns ctx's error when ctx ends while it
// waits for the disk.
func AtStart(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	disk, err := waitDisk(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		refused(ctx, cfg, "", err)
		return nil
	}
	defer disk.Close()
	if err := cfg.Platform.PrepareDir(cfg.Dir); err != nil {
		refused(ctx, cfg, "", err)
		return nil
	}
	installed, diskVersion, err := install(ctx, cfg, disk)
	if err != nil {
		refused(ctx, cfg, diskVersion, err)
		return nil
	}
	if !installed {
		return nil
	}
	path := filepath.Join(cfg.Dir, SelfName)
	err = &Refusal{Rule: RuleExecFailed, err: cfg.Platform.Exec(path)}
	cfg.Log.LogAttrs(ctx, slog.LevelError, "update not run",
		slog.String("rule", RuleExecFailed), slog.String("decision", "exit"),
		slog.String("path", path), slog.String("error", err.Error()))
	return err
}

// waitDisk opens the disk until its check passes, every pollEvery, for
// at most cfg.Wait. It returns the last refusal when the wait ends, and
// at once when the platform has no tools disk.
func waitDisk(ctx context.Context, cfg Config) (*os.Root, error) {
	start := time.Now()
	logged := false
	for {
		disk, err := cfg.Platform.OpenDisk(cfg.Disk)
		if err == nil {
			if logged {
				cfg.Log.LogAttrs(ctx, slog.LevelInfo, "tools disk mounted",
					slog.String("path", cfg.Disk), slog.Duration("waited", time.Since(start)))
			}
			return disk, nil
		}
		if errors.Is(err, errors.ErrUnsupported) || time.Since(start) >= cfg.Wait {
			return nil, err
		}
		if !logged {
			cfg.Log.LogAttrs(ctx, slog.LevelInfo, "waiting for the tools disk",
				slog.String("path", cfg.Disk), slog.Duration("wait", cfg.Wait), slog.String("error", err.Error()))
			logged = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

// refused logs a refusal of the update, with the rule of err. Every
// error AtStart passes here holds its rule, or wraps
// errors.ErrUnsupported.
func refused(ctx context.Context, cfg Config, diskVersion string, err error) {
	rule := RuleInstallFailed
	var ur *Refusal
	var tr *Refusal
	switch {
	case errors.As(err, &ur):
		rule = ur.Rule
	case errors.As(err, &tr):
		rule = tr.Rule
	case errors.Is(err, errors.ErrUnsupported):
		rule = RuleUnsupported
	}
	attrs := []slog.Attr{
		slog.String("rule", rule), slog.String("decision", "serve as it is"),
		slog.String("version", label(cfg.Own)),
	}
	if diskVersion != "" {
		attrs = append(attrs, slog.String("disk_version", label(diskVersion)))
	}
	cfg.Log.LogAttrs(ctx, slog.LevelWarn, "update refused", append(attrs, slog.String("error", err.Error()))...)
}


// program is a program of the disk, open for reading.
type program struct {
	name string
	f    *os.File
	size int64
}

// install reads the disk's VERSION and, when it isn't cfg.Own, checks
// every program on the disk before it installs any, then installs them
// in cfg.Programs' order. It returns whether it installed them, and the
// disk's version once it has read a valid one.
func install(ctx context.Context, cfg Config, disk *os.Root) (installed bool, diskVersion string, err error) {
	diskVersion, err = readVersion(disk)
	if err != nil {
		return false, "", err
	}
	if diskVersion == cfg.Own {
		cfg.Log.LogAttrs(ctx, slog.LevelInfo, "tools disk is current", slog.String("version", label(cfg.Own)))
		return false, diskVersion, nil
	}
	progs, err := openPrograms(disk, cfg.Programs, cfg.MaxSize)
	defer func() {
		for _, p := range progs {
			_ = p.f.Close()
		}
	}()
	if err != nil {
		return false, diskVersion, err
	}
	dir, err := os.OpenRoot(cfg.Dir)
	if err != nil {
		return false, diskVersion, &Refusal{Rule: RuleInstallFailed, err: err}
	}
	defer dir.Close()
	if self := progs[len(progs)-1]; sameBytes(dir, self) {
		return false, diskVersion, &Refusal{Rule: RuleNoEffect, err: errors.New(
			"the installed " + SelfName + " is the disk's, but its version isn't the disk's VERSION")}
	}
	for _, p := range progs {
		if err := installOne(dir, p); err != nil {
			return false, diskVersion, &Refusal{Rule: RuleInstallFailed, err: err}
		}
	}
	cfg.Log.LogAttrs(ctx, slog.LevelInfo, "update installed",
		slog.String("from", label(cfg.Own)), slog.String("to", label(diskVersion)),
		slog.Any("programs", cfg.Programs), slog.String("dir", cfg.Dir))
	return true, diskVersion, nil
}

// readVersion reads the disk's VERSION: a regular file that holds a
// build version and nothing else.
func readVersion(disk *os.Root) (string, error) {
	f, _, err := openRegular(disk, "VERSION", 64)
	if err != nil {
		return "", &Refusal{Rule: RuleVersionInvalid, err: err}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64+1))
	if err != nil {
		return "", &Refusal{Rule: RuleVersionInvalid, err: err}
	}
	if v := string(b); validVersion(v) {
		return v, nil
	}
	return "", &Refusal{Rule: RuleVersionInvalid, err: fmt.Errorf("%s holds %s, not a build version",
		"VERSION", label(string(b)))}
}

// openPrograms opens each program of names on the disk. It requires
// SelfName last, so wb-guestd is installed after the others. It returns
// the programs it opened, also on an error, for the caller to close.
func openPrograms(disk *os.Root, names []string, maxSize int64) ([]program, error) {
	if len(names) == 0 || names[len(names)-1] != SelfName {
		return nil, &Refusal{Rule: RuleProgramInvalid, err: fmt.Errorf("the programs %q don't end in %s", names, SelfName)}
	}
	progs := make([]program, 0, len(names))
	for _, name := range names {
		f, fi, err := openRegular(disk, name, maxSize)
		if err != nil {
			return progs, &Refusal{Rule: RuleProgramInvalid, err: err}
		}
		progs = append(progs, program{name: name, f: f, size: fi.Size()})
	}
	return progs, nil
}

// openRegular opens name in root when it is a regular file of at most
// maxSize bytes. A symbolic link is refused, not followed.
func openRegular(root *os.Root, name string, maxSize int64) (*os.File, fs.FileInfo, error) {
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: not a regular file: %v", name, fi.Mode().Type())
	}
	if fi.Size() > maxSize {
		return nil, nil, fmt.Errorf("%s: %d bytes, over the cap of %d", name, fi.Size(), maxSize)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err == nil && !os.SameFile(fi, st) {
		err = fmt.Errorf("%s: changed while it was opened", name)
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, st, nil
}

// sameBytes reports whether p's file in dir holds the same bytes as p.
func sameBytes(dir *os.Root, p program) bool {
	f, fi, err := openRegular(dir, p.name, p.size)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi.Size() != p.size {
		return false
	}
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	a, b := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, errA := io.ReadFull(p.f, a)
		nb, errB := io.ReadFull(f, b)
		if na != nb || !bytes.Equal(a[:na], b[:nb]) {
			return false
		}
		if errA != nil || errB != nil {
			return atEnd(errA) && atEnd(errB)
		}
	}
}

// atEnd reports whether err from io.ReadFull means the reader ended.
func atEnd(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// installOne copies p into dir under a temporary name, created
// exclusively, makes it mode 0755 and syncs it, then renames it over
// p.name. A link at either name is replaced, not followed. The file
// belongs to wb-guestd's user, root. A crash before the rename leaves
// the old program, which the next start replaces.
func installOne(dir *os.Root, p program) error {
	tmp := "." + p.name + ".new"
	if err := dir.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	err = writeProgram(f, p)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = dir.Rename(tmp, p.name)
	}
	if err != nil {
		_ = dir.Remove(tmp)
	}
	return err
}

func writeProgram(f *os.File, p program) error {
	n, err := io.Copy(f, io.LimitReader(p.f, p.size+1))
	if err != nil {
		return err
	}
	if n != p.size {
		return fmt.Errorf("%s: read %d bytes, want %d", p.name, n, p.size)
	}
	if err := f.Chmod(0o755); err != nil {
		return err
	}
	return f.Sync()
}
