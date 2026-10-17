package localipc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// xucredVersion is XUCRED_VERSION from <sys/ucred.h>, the layout of
// struct xucred this code reads. golang.org/x/sys/unix doesn't define it.
const xucredVersion = 0

// probeTimeout bounds the connect that tells a leftover socket from a
// live one at Listen.
const probeTimeout = time.Second

var (
	// lstat is os.Lstat. The tests replace it for a directory of another
	// owner, which needs root to make.
	lstat = os.Lstat
	// peerCredential reads the peer of a connected Unix socket. The tests
	// replace it for a peer of another user ID.
	peerCredential = readPeerCredential
)

// readPeerCredential reads LOCAL_PEERCRED and LOCAL_PEERPID. A
// credential of another xucred version is an error, so a layout this
// code doesn't know is never read as a user ID.
func readPeerCredential(c *net.UnixConn) (Peer, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var (
		cred  *unix.Xucred
		pid   int
		opErr error
	)
	err = rc.Control(func(fd uintptr) {
		cred, opErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if opErr != nil {
			opErr = fmt.Errorf("LOCAL_PEERCRED: %w", opErr)
			return
		}
		pid, opErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if opErr != nil {
			opErr = fmt.Errorf("LOCAL_PEERPID: %w", opErr)
		}
	})
	if err != nil {
		return Peer{}, err
	}
	if opErr != nil {
		return Peer{}, opErr
	}
	if cred.Version != xucredVersion {
		return Peer{}, fmt.Errorf("LOCAL_PEERCRED: xucred version %d, want %d", cred.Version, xucredVersion)
	}
	return Peer{UID: int(cred.Uid), PID: pid}, nil
}

// EnsureRunDir makes the run directory with mode 0700 when it is
// missing, and its missing parents with 0700 too, then checks it with
// CheckRunDir. A run directory that fails the check is refused with
// RuleRunDirUnsafe and left as it is: a wrong mode or owner means
// something else wrote there, so it isn't repaired (S04-architecture,
// "Inter-process contracts"; SEC12-least-privilege).
func EnsureRunDir(log *slog.Logger, dir string) error {
	if !filepath.IsAbs(dir) {
		return refuse(log, &Refusal{Rule: RuleRunDirUnsafe, msg: fmt.Sprintf("run directory %q is not an absolute path", dir)})
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return refuse(log, &Refusal{Rule: RuleRunDirUnsafe, msg: fmt.Sprintf("make run directory %q", dir), err: err})
	}
	return CheckRunDir(log, dir)
}

// CheckRunDir checks the run directory without changing it: a directory,
// not a symbolic link, owned by the effective user ID, with no group or
// other permission bits. Anything else is refused with RuleRunDirUnsafe.
func CheckRunDir(log *slog.Logger, dir string) error {
	attrs := []slog.Attr{slog.String("dir", dir)}
	fi, err := lstat(dir)
	if err != nil {
		return refuse(log, &Refusal{Rule: RuleRunDirUnsafe, msg: fmt.Sprintf("lstat run directory %q", dir), err: err}, attrs...)
	}
	uid, owned := ownerOf(fi)
	var why string
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		why = "is a symbolic link"
	case !fi.IsDir():
		why = "is not a directory"
	case !owned:
		why = "has no owner the code can read"
	case uid != ownUID():
		why = fmt.Sprintf("is owned by user ID %d, not %d", uid, ownUID())
	case fi.Mode().Perm()&0o077 != 0:
		why = fmt.Sprintf("has mode %04o, with group or other permission bits", fi.Mode().Perm())
	default:
		return nil
	}
	return refuse(log, &Refusal{Rule: RuleRunDirUnsafe, msg: fmt.Sprintf("run directory %q %s", dir, why)}, attrs...)
}

// ownerOf returns the owner of an lstat result.
func ownerOf(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// Listen listens at <dir>/<name>.sock, where dir is the run directory
// (paths.Paths.Run) and name matches [a-z][a-z0-9-]*. It makes and
// checks the run directory with EnsureRunDir, clears a leftover socket
// from a crash, binds, and gives the socket file mode 0600. The
// directory mode is the control. The file mode is a second layer.
func Listen(ctx context.Context, log *slog.Logger, dir, name string) (*Listener, error) {
	path, err := endpointPath(log, dir, name)
	if err != nil {
		return nil, err
	}
	if err := EnsureRunDir(log, dir); err != nil {
		return nil, err
	}
	lock, err := lockEndpoint(log, path)
	if err != nil {
		return nil, err
	}
	ul, err := bind(ctx, log, path)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	logger(log).LogAttrs(ctx, slog.LevelInfo, "local IPC listening", slog.String("endpoint", path))
	return &Listener{ln: ul, lock: lock, log: log, endpoint: path, peerOf: peerCredential}, nil
}

// bind clears a leftover socket and binds at path, then gives the
// socket file mode 0600. The caller holds the endpoint's lock.
func bind(ctx context.Context, log *slog.Logger, path string) (*net.UnixListener, error) {
	if err := clearLeftover(ctx, log, path); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("localipc: listen at %q: %w", path, err)
	}
	ul, ok := ln.(*net.UnixListener)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("localipc: listen at %q gave a %T", path, ln)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ul.Close()
		return nil, fmt.Errorf("localipc: chmod 0600 %q: %w", path, err)
	}
	return ul, nil
}

// lockEndpoint takes an exclusive flock on <dir>/<name>.lock without
// waiting, and returns the open file that holds it. The listener holds
// it for its life, so only one listener at a time owns the endpoint
// path: only the holder removes a leftover socket, and only the holder's
// Close unlinks the socket. A connect refused with ECONNREFUSED doesn't
// prove a leftover on its own: a live listener with a full backlog
// refuses too. A lock another process holds is refused with
// RuleEndpointInUse. The lock file is never removed, so two listeners
// always lock the same file. A lock file that is a symbolic link, or not
// a regular file of the effective user, is refused with
// RuleEndpointUnsafe.
func lockEndpoint(log *slog.Logger, path string) (*os.File, error) {
	lockPath := strings.TrimSuffix(path, ".sock") + ".lock"
	attrs := []slog.Attr{slog.String("endpoint", path), slog.String("lock", lockPath)}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, refuse(log, &Refusal{Rule: RuleEndpointUnsafe, msg: "open the endpoint's lock file", err: err}, attrs...)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("localipc: stat the lock file %q: %w", lockPath, err)
	}
	if uid, owned := ownerOf(fi); !fi.Mode().IsRegular() || !owned || uid != ownUID() {
		_ = f.Close()
		return nil, refuse(log, &Refusal{Rule: RuleEndpointUnsafe, msg: "the endpoint's lock file is not a regular file of the effective user"}, attrs...)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, refuse(log, &Refusal{Rule: RuleEndpointInUse, msg: "another listener holds the endpoint's lock"}, attrs...)
		}
		return nil, fmt.Errorf("localipc: flock %q: %w", lockPath, err)
	}
	return f, nil
}

// clearLeftover handles a file already at the endpoint path. The caller
// holds the endpoint's lock, so no other listener of localipc owns the
// path. A socket of the effective user whose connect is refused is a
// leftover from a crash, and is removed. One where a connect succeeds is a live
// listener: RuleEndpointInUse. Anything else, a regular file, a symbolic
// link, or a socket of another owner, is refused with RuleEndpointUnsafe
// and left in place.
func clearLeftover(ctx context.Context, log *slog.Logger, path string) error {
	attrs := []slog.Attr{slog.String("endpoint", path)}
	fi, err := lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return refuse(log, &Refusal{Rule: RuleEndpointUnsafe, msg: "lstat the endpoint path", err: err}, attrs...)
	}
	if fi.Mode().Type() != fs.ModeSocket {
		return refuse(log, &Refusal{Rule: RuleEndpointUnsafe, msg: fmt.Sprintf("the endpoint path holds a file of type %s, not a socket", fi.Mode().Type())}, attrs...)
	}
	if uid, ok := ownerOf(fi); !ok || uid != ownUID() {
		return refuse(log, &Refusal{Rule: RuleEndpointUnsafe, msg: fmt.Sprintf("the socket at the endpoint path is owned by user ID %d, not %d", uid, ownUID())}, attrs...)
	}
	d := net.Dialer{Timeout: probeTimeout}
	c, err := d.DialContext(ctx, "unix", path)
	if err == nil {
		_ = c.Close()
		return refuse(log, &Refusal{Rule: RuleEndpointInUse, msg: "a listener answers at the endpoint path"}, attrs...)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return refuse(log, &Refusal{Rule: RuleEndpointInUse, msg: "probe the socket at the endpoint path", err: err}, attrs...)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("localipc: remove the leftover socket %q: %w", path, err)
	}
	logger(log).LogAttrs(ctx, slog.LevelInfo, "local IPC leftover socket removed",
		append(attrs, slog.String("decision", "remove"))...)
	return nil
}

// Dial connects to <dir>/<name>.sock. It checks the run directory with
// CheckRunDir first, and after the connect it checks that the listener
// has the effective user ID, refusing another with RulePeerUID
// (S04-architecture, "Inter-process contracts").
func Dial(ctx context.Context, log *slog.Logger, dir, name string) (*Conn, error) {
	path, err := endpointPath(log, dir, name)
	if err != nil {
		return nil, err
	}
	if err := CheckRunDir(log, dir); err != nil {
		return nil, err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("localipc: dial %q: %w", path, err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("localipc: dial %q gave a %T", path, c)
	}
	peer, err := checkPeer(log, peerCredential, uc, path, "dial")
	if err != nil {
		_ = uc.Close()
		return nil, err
	}
	return &Conn{UnixConn: uc, peer: peer}, nil
}

// LockHeld reports whether a listener holds the lock of the endpoint
// <dir>/<name>.sock: whether one listens there, or is about to. A dialer
// that gets ECONNREFUSED asks it, because a live listener with a full
// backlog refuses a connect too, so only the lock tells it from a
// leftover socket. A missing lock file, or a missing run directory, is
// not held. When no one holds the lock, LockHeld takes it for an
// instant, so a Listen in that instant is refused with
// RuleEndpointInUse. It opens the lock file read-only and without
// waiting, never creates it, and doesn't follow a symbolic link. A lock
// file that isn't a regular file of the effective user, such as a FIFO
// that would block the open, is refused with RuleEndpointUnsafe, as
// Listen refuses it.
func LockHeld(log *slog.Logger, dir, name string) (bool, error) {
	path, err := endpointPath(log, dir, name)
	if err != nil {
		return false, err
	}
	lockPath := strings.TrimSuffix(path, ".sock") + ".lock"
	attrs := []slog.Attr{slog.String("endpoint", path), slog.String("lock", lockPath)}
	f, err := os.OpenFile(lockPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, refuse(log, &Refusal{Rule: RuleEndpointUnsafe, msg: "open the endpoint's lock file", err: err}, attrs...)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("localipc: stat the lock file %q: %w", lockPath, err)
	}
	if uid, owned := ownerOf(fi); !fi.Mode().IsRegular() || !owned || uid != ownUID() {
		return false, refuse(log, &Refusal{Rule: RuleEndpointUnsafe, msg: "the endpoint's lock file is not a regular file of the effective user"}, attrs...)
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("localipc: flock %q: %w", lockPath, err)
	}
	// Closing the file releases the lock.
	return false, nil
}

// endpointPath returns <dir>/<name>.sock, refusing a name outside
// [a-z][a-z0-9-]* and a path over MaxPathLen bytes.
func endpointPath(log *slog.Logger, dir, name string) (string, error) {
	if !validName(name) {
		return "", refuse(log, &Refusal{Rule: RuleEndpointName, msg: fmt.Sprintf("endpoint name %q is not [a-z][a-z0-9-]*", name)})
	}
	path := filepath.Join(dir, name+".sock")
	if len(path) > MaxPathLen {
		return "", refuse(log, &Refusal{Rule: RulePathTooLong, msg: fmt.Sprintf("endpoint path %q is %d bytes, over %d", path, len(path), MaxPathLen)})
	}
	return path, nil
}

func validName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		b := name[i]
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '-' {
			return false
		}
	}
	return true
}
