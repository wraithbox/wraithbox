package guestmount

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Diskutil is the program System runs, at its fixed path, never from
// PATH.
const Diskutil = "/usr/sbin/diskutil"

// diskutilEnv is diskutil's whole environment.
var diskutilEnv = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C"}

// The bounds of a diskutil run. A volume's report is a few KiB.
const (
	infoTimeout  = 10 * time.Second
	mountTimeout = 30 * time.Second
	maxOutput    = 1 << 20
	maxErrOutput = 4 << 10
	// outputWait is how long a run waits for diskutil's output after it
	// exits or its deadline passes.
	outputWait = 5 * time.Second
)

// System makes the calls on the guest it runs in.
type System struct {
	// Log gets each diskutil run. nil takes slog.Default().
	Log *slog.Logger
}

func (s *System) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// errNotFound is the error of Info for a volume diskutil doesn't find.
var errNotFound = errors.New("guestmount: no such volume")

// Info runs `diskutil info -plist <uuid>`, and reports the volume. found
// is false when diskutil fails, which is how it answers for a UUID no
// volume has.
func (s *System) Info(ctx context.Context, uuid string) (Volume, bool, error) {
	out, err := s.run(ctx, infoTimeout, "info", "-plist", uuid)
	if errors.Is(err, errNotFound) {
		return Volume{}, false, nil
	}
	if err != nil {
		return Volume{}, false, err
	}
	vol, err := ParseInfo(out)
	if err != nil {
		return Volume{}, false, err
	}
	return vol, true, nil
}

// Unmount runs `diskutil unmount <device>`.
func (s *System) Unmount(ctx context.Context, dev string) error {
	if !ValidDevice(dev) {
		return fmt.Errorf("guestmount: %q isn't a volume", cut(dev))
	}
	_, err := s.run(ctx, mountTimeout, "unmount", dev)
	return err
}

// Mount runs `diskutil mount -mountOptions <MountOptions> -mountPoint
// <point> <device>`.
func (s *System) Mount(ctx context.Context, dev, point string) error {
	if !ValidDevice(dev) {
		return fmt.Errorf("guestmount: %q isn't a volume", cut(dev))
	}
	_, err := s.run(ctx, mountTimeout, "mount", "-mountOptions", MountOptions, "-mountPoint", point, dev)
	return err
}

// Statfs reports the file system that holds path.
func (*System) Statfs(path string) (Mount, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Mount{}, fmt.Errorf("guestmount: statfs %s: %w", path, err)
	}
	return Mount{
		From:            unix.ByteSliceToString(st.Mntfromname[:]),
		On:              unix.ByteSliceToString(st.Mntonname[:]),
		NoSUID:          st.Flags&unix.MNT_NOSUID != 0,
		NoDev:           st.Flags&unix.MNT_NODEV != 0,
		DontBrowse:      st.Flags&unix.MNT_DONTBROWSE != 0,
		IgnoreOwnership: st.Flags&unix.MNT_IGNORE_OWNERSHIP != 0,
	}, nil
}

// Lstat reports path without following a link. A missing path is an
// error that wraps fs.ErrNotExist.
func (*System) Lstat(path string) (Dir, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return Dir{}, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	return dirOf(&st), nil
}

func dirOf(st *unix.Stat_t) Dir {
	kind := uint32(st.Mode) & unix.S_IFMT
	return Dir{
		IsDir: kind == unix.S_IFDIR, IsLink: kind == unix.S_IFLNK,
		UID: st.Uid, GID: st.Gid,
		Perm:    fs.FileMode(st.Mode) & fs.ModePerm,
		Special: uint32(st.Mode)&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) != 0,
	}
}

// Mkdir makes path a directory owned by root:wheel with mode 755. It
// fails when path exists, a link included, and never follows one.
func (s *System) Mkdir(path string) error {
	if err := unix.Mkdir(path, 0o700); err != nil {
		return &fs.PathError{Op: "mkdir", Path: path, Err: err}
	}
	return s.Claim(path)
}

// Claim makes the directory at path owned by root:wheel with mode 755,
// with fchown and fchmod on a descriptor opened with O_NOFOLLOW, so a
// link put in its place is never followed (S06-05-disks, "Owners and
// modes").
func (*System) Claim(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return &fs.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	if err := unix.Fchown(fd, 0, 0); err != nil {
		return &fs.PathError{Op: "fchown", Path: path, Err: err}
	}
	if err := unix.Fchmod(fd, 0o755); err != nil {
		return &fs.PathError{Op: "fchmod", Path: path, Err: err}
	}
	return nil
}

// run runs diskutil with args and the fixed environment, within
// timeout, and returns its standard output. A failed `info` is
// errNotFound. It logs the run.
func (s *System) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, Diskutil, args...)
	cmd.Env = diskutilEnv
	cmd.WaitDelay = outputWait
	stdout := &boundedBuffer{max: maxOutput}
	stderr := &boundedBuffer{max: maxErrOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err := cmd.Run()
	attrs := []slog.Attr{
		slog.String("program", Diskutil), slog.String("args", strings.Join(args, " ")),
		slog.Duration("took", time.Since(start)), slog.String("stderr", stderr.String()),
	}
	if err == nil && stdout.over {
		err = fmt.Errorf("guestmount: diskutil wrote more than %d bytes", maxOutput)
	}
	if err != nil && args[0] == "info" && ctx.Err() == nil {
		// diskutil answers a UUID no volume has with an exit status of
		// 1, which a volume that hasn't appeared yet gets too.
		s.log().LogAttrs(ctx, slog.LevelDebug, "diskutil found no volume", attrs...)
		return nil, errNotFound
	}
	if err != nil {
		s.log().LogAttrs(ctx, slog.LevelError, "diskutil failed", append(attrs, slog.String("error", err.Error()))...)
		return nil, fmt.Errorf("diskutil %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	// A volume's report is read before every session start, so it logs
	// at debug level.
	level := slog.LevelInfo
	if args[0] == "info" {
		level = slog.LevelDebug
	}
	s.log().LogAttrs(ctx, level, "diskutil ran", attrs...)
	return stdout.Bytes(), nil
}

// boundedBuffer keeps the first max bytes written to it, and drops the
// rest, so diskutil never blocks on a full pipe.
type boundedBuffer struct {
	bytes.Buffer

	max  int
	over bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.Len()
	if len(p) > room {
		b.over = true
		if room > 0 {
			_, _ = b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}
