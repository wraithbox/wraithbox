package localipc

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a log sink the listener's goroutine and the test share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testLog() (*slog.Logger, *syncBuffer) {
	var buf syncBuffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// shortDir returns a temporary <data> whose run/<name>.sock fits in
// sun_path. t.TempDir's path holds the test name, which can make it too
// long.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lipc") //nolint:usetesting // t.TempDir's path holds the test name, which pushes run/<name>.sock past sun_path's 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func wantRule(t *testing.T, err error, rule Rule) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != rule {
		t.Fatalf("err = %v, want %s", err, rule)
	}
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()
}

func TestEnsureRunDirMakes0700(t *testing.T) {
	run := filepath.Join(shortDir(t), "missing", "parent", "run")
	if err := EnsureRunDir(nil, run); err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	for _, dir := range []string{run, filepath.Dir(run), filepath.Dir(filepath.Dir(run))} {
		if got := modeOf(t, dir).Perm(); got != 0o700 {
			t.Errorf("%s mode = %04o, want 0700", dir, got)
		}
	}
	if err := EnsureRunDir(nil, run); err != nil {
		t.Errorf("EnsureRunDir on its own directory: %v", err)
	}
}

func TestEnsureRunDirRefuses(t *testing.T) {
	data := shortDir(t)
	open := filepath.Join(data, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(data, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(data, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(data, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, dir string
		mode      fs.FileMode
	}{
		{"mode 0755", open, fs.ModeDir | 0o755},
		{"symbolic link", link, fs.ModeSymlink},
		{"regular file", file, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			log, buf := testLog()
			wantRule(t, EnsureRunDir(log, c.dir), RuleRunDirUnsafe)
			if !strings.Contains(buf.String(), `"rule":"ipc-run-dir-unsafe"`) {
				t.Errorf("log = %s, want the rule", buf.String())
			}
			if got := modeOf(t, c.dir); !sameMode(got, c.mode) {
				t.Errorf("%s mode after the refusal = %v, want %v unchanged", c.dir, got, c.mode)
			}
		})
	}
	wantRule(t, EnsureRunDir(nil, "relative/run"), RuleRunDirUnsafe)
}

// sameMode compares the file types, and for a directory the permissions.
func sameMode(got, want fs.FileMode) bool {
	if got.Type() != want.Type() {
		return false
	}
	return !got.IsDir() || got.Perm() == want.Perm()
}

// otherOwner is an lstat result for a directory of another user, which
// needs root to make.
type otherOwner struct{ fs.FileInfo }

func (o otherOwner) Sys() any {
	st, ok := o.FileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	other := *st
	other.Uid++
	return &other
}

func TestCheckRunDirOtherOwner(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	if err := EnsureRunDir(nil, run); err != nil {
		t.Fatal(err)
	}
	old := lstat
	lstat = func(name string) (fs.FileInfo, error) {
		fi, err := os.Lstat(name)
		if err != nil {
			return nil, err
		}
		return otherOwner{fi}, nil
	}
	t.Cleanup(func() { lstat = old })
	wantRule(t, EnsureRunDir(nil, run), RuleRunDirUnsafe)
	wantRule(t, CheckRunDir(nil, run), RuleRunDirUnsafe)
}

func TestListenRefusesNames(t *testing.T) {
	ctx := context.Background()
	run := filepath.Join(shortDir(t), "run")
	for _, name := range []string{"", "Hostd", "1x", "a/b", "a.b", "../x", "-a"} {
		_, err := Listen(ctx, nil, run, name)
		wantRule(t, err, RuleEndpointName)
	}
	long := strings.Repeat("a", MaxPathLen)
	_, err := Listen(ctx, nil, run, long)
	wantRule(t, err, RulePathTooLong)
	_, err = Dial(ctx, nil, run, long)
	wantRule(t, err, RulePathTooLong)
}

// TestPathBound: a path of exactly MaxPathLen bytes binds, one byte more
// is refused.
func TestPathBound(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	name := strings.Repeat("a", MaxPathLen-len(run)-len("/.sock"))
	ln, err := Listen(context.Background(), nil, run, name)
	if err != nil {
		t.Fatalf("Listen with a %d-byte path: %v", MaxPathLen, err)
	}
	_ = ln.Close()
	_, err = Listen(context.Background(), nil, run, name+"a")
	wantRule(t, err, RulePathTooLong)
}

func listen(t *testing.T, log *slog.Logger, run string) *Listener {
	t.Helper()
	ln, err := Listen(context.Background(), log, run, "hostd")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func TestListenSocketMode(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	listen(t, nil, run)
	if got := modeOf(t, filepath.Join(run, "hostd.sock")); got.Type() != fs.ModeSocket || got.Perm() != 0o600 {
		t.Errorf("socket mode = %v, want a socket with 0600", got)
	}
}

// leftover leaves a socket file with no listener, as a crash does.
func leftover(t *testing.T, path string) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close()
}

func TestListenReplacesLeftover(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	if err := EnsureRunDir(nil, run); err != nil {
		t.Fatal(err)
	}
	leftover(t, filepath.Join(run, "hostd.sock"))
	log, buf := testLog()
	ln := listen(t, log, run)
	if !strings.Contains(buf.String(), "leftover socket removed") {
		t.Errorf("log = %s, want the removal", buf.String())
	}
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	c, err := Dial(context.Background(), nil, run, "hostd")
	if err != nil {
		t.Fatalf("Dial the new listener: %v", err)
	}
	_ = c.Close()
}

func TestListenRefusesLive(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	listen(t, nil, run)
	_, err := Listen(context.Background(), nil, run, "hostd")
	wantRule(t, err, RuleEndpointInUse)
}

func TestListenRefusesUnsafeFile(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	if err := EnsureRunDir(nil, run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(run, "hostd.sock")
	for _, c := range []struct {
		name string
		make func() error
		mode fs.FileMode
	}{
		{"regular file", func() error { return os.WriteFile(path, []byte("x"), 0o600) }, 0},
		{"symbolic link", func() error { return os.Symlink(filepath.Join(run, "elsewhere"), path) }, fs.ModeSymlink},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.make(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(path) })
			_, err := Listen(context.Background(), nil, run, "hostd")
			wantRule(t, err, RuleEndpointUnsafe)
			if got := modeOf(t, path).Type(); got != c.mode {
				t.Errorf("file type after the refusal = %v, want %v left in place", got, c.mode)
			}
		})
	}
}

func TestListenRefusesOtherOwnersSocket(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	if err := EnsureRunDir(nil, run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(run, "hostd.sock")
	leftover(t, path)
	old := lstat
	lstat = func(name string) (fs.FileInfo, error) {
		fi, err := os.Lstat(name)
		if err != nil || name != path {
			return fi, err
		}
		return otherOwner{fi}, nil
	}
	t.Cleanup(func() { lstat = old })
	_, err := Listen(context.Background(), nil, run, "hostd")
	wantRule(t, err, RuleEndpointUnsafe)
	if got := modeOf(t, path).Type(); got != fs.ModeSocket {
		t.Errorf("socket after the refusal: type %v, want it left in place", got)
	}
}

// TestSameUserPeer: a real connection of the same user is accepted on
// both ends, each with the other's user ID and process ID.
func TestSameUserPeer(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	ln := listen(t, nil, run)
	accepted := make(chan *Conn, 1)
	go func() {
		c, err := ln.AcceptPeer()
		if err != nil {
			t.Errorf("AcceptPeer: %v", err)
		}
		accepted <- c
	}()
	c, err := Dial(context.Background(), nil, run, "hostd")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	s := <-accepted
	if s == nil {
		t.FailNow()
	}
	defer s.Close()
	want := Peer{UID: os.Geteuid(), PID: os.Getpid()}
	if got := s.Peer(); got != want {
		t.Errorf("listener's peer = %+v, want %+v", got, want)
	}
	if got := c.Peer(); got != want {
		t.Errorf("dialer's peer = %+v, want %+v", got, want)
	}
}

// stubPeers makes peerCredential report another user ID for the first n
// calls, then the real credential.
func stubPeers(t *testing.T, n int) {
	t.Helper()
	var mu sync.Mutex
	old := peerCredential
	peerCredential = func(c *net.UnixConn) (Peer, error) {
		mu.Lock()
		defer mu.Unlock()
		p, err := readPeerCredential(c)
		if n > 0 {
			n--
			p.UID++
		}
		return p, err
	}
	t.Cleanup(func() { peerCredential = old })
}

// TestPeerOtherUID: a peer of another user ID is closed with RulePeerUID
// before it gets a byte, and the next connection is still accepted
// (SEC12-least-privilege).
func TestPeerOtherUID(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	log, buf := testLog()
	stubPeers(t, 1)
	ln := listen(t, log, run)
	accepted := make(chan *Conn, 1)
	go func() {
		c, err := ln.AcceptPeer()
		if err != nil {
			t.Errorf("AcceptPeer: %v", err)
		}
		accepted <- c
	}()
	path := filepath.Join(run, "hostd.sock")
	var d net.Dialer
	refused, err := d.DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Close()
	_ = refused.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := refused.Read(make([]byte, 1)); n != 0 || err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("refused peer read %d bytes, %v; want the connection closed", n, err)
	}
	next, err := d.DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	s := <-accepted
	if s == nil {
		t.FailNow()
	}
	defer s.Close()
	if s.Peer().UID != os.Geteuid() {
		t.Errorf("accepted peer %+v, want the second connection's", s.Peer())
	}
	log0 := buf.String()
	for _, want := range []string{`"rule":"ipc-peer-uid"`, `"peer_uid":`, `"peer_pid":`} {
		if !strings.Contains(log0, want) {
			t.Errorf("log = %s, want %s", log0, want)
		}
	}
}

// TestDialOtherUID: the dialer refuses a listener of another user ID.
func TestDialOtherUID(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	ln := listen(t, nil, run)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	stubPeers(t, 1)
	_, err := Dial(context.Background(), nil, run, "hostd")
	wantRule(t, err, RulePeerUID)
}

// TestPeerCredentialUnreadable: a peer whose credential can't be read is
// refused too.
func TestPeerCredentialUnreadable(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	ln := listen(t, nil, run)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	old := peerCredential
	peerCredential = func(*net.UnixConn) (Peer, error) { return Peer{}, errors.New("no credential") }
	t.Cleanup(func() { peerCredential = old })
	_, err := Dial(context.Background(), nil, run, "hostd")
	wantRule(t, err, RulePeerUID)
}

func TestDialRefusesUnsafeRunDir(t *testing.T) {
	run := filepath.Join(shortDir(t), "run")
	if err := os.Mkdir(run, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Dial(context.Background(), nil, run, "hostd")
	wantRule(t, err, RuleRunDirUnsafe)
}
