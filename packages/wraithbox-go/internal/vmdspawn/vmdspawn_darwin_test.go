package vmdspawn_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdspawn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmdspawn/vmdfake"
)

// own is the build version in these tests, passed explicitly
// (S10-02-packaging-and-signing, "One build version").
const own = "1.2.3-dev+abc"

// TestMain runs the fake wb-vmd when the test binary is started as
// wb-vmd: its whole environment is the slot. The fake checks its start
// interface first, and exits 3 when it is wrong.
func TestMain(m *testing.M) {
	if vmdfake.Is() {
		os.Exit(vmdfake.Main(own, checkStartInterface))
	}
	os.Exit(m.Run())
}

// checkStartInterface checks, inside wb-vmd, the start interface of
// S04-03-self-sandboxing, "How `wb-vmd` is started": no arguments,
// WB_VMD_SLOT=work or WB_VMD_SLOT=isolated as the whole environment, a
// blocking lifeline, and descriptors 0, 2, 3 and 4 and no others.
func checkStartInterface() error {
	if len(os.Args) != 1 {
		return fmt.Errorf("arguments %q", os.Args[1:])
	}
	if env := os.Environ(); len(env) != 1 || (env[0] != "WB_VMD_SLOT=work" && env[0] != "WB_VMD_SLOT=isolated") {
		return fmt.Errorf("environment %q", env)
	}
	if fl, err := unix.FcntlInt(0, unix.F_GETFL, 0); err != nil || fl&unix.O_NONBLOCK != 0 {
		return fmt.Errorf("fd 0: flags %#x: %w", fl, err)
	}
	if err := checkKinds(); err != nil {
		return err
	}
	if err := checkGRPCSocket(); err != nil {
		return err
	}
	for fd := 5; fd < 256; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			return fmt.Errorf("fd %d is open", fd)
		}
	}
	return nil
}

// checkKinds checks that 0 and 2 are pipes, 3 a stream socket and 4 a
// datagram socket.
func checkKinds() error {
	for _, c := range []struct {
		fd, kind uint32
	}{{0, syscall.S_IFIFO}, {2, syscall.S_IFIFO}, {3, syscall.S_IFSOCK}, {4, syscall.S_IFSOCK}} {
		var st syscall.Stat_t
		if err := syscall.Fstat(int(c.fd), &st); err != nil || uint32(st.Mode)&syscall.S_IFMT != c.kind {
			return fmt.Errorf("fd %d: mode %#o: %w", c.fd, st.Mode, err)
		}
	}
	if typ, _ := syscall.GetsockoptInt(3, syscall.SOL_SOCKET, syscall.SO_TYPE); typ != syscall.SOCK_STREAM {
		return fmt.Errorf("fd 3: socket type %d", typ)
	}
	if typ, _ := syscall.GetsockoptInt(4, syscall.SOL_SOCKET, syscall.SO_TYPE); typ != syscall.SOCK_DGRAM {
		return fmt.Errorf("fd 4: socket type %d", typ)
	}
	return nil
}

// checkGRPCSocket checks that fd 3 is bound to a socket file of mode
// 0600.
func checkGRPCSocket() error {
	sa, err := syscall.Getsockname(3)
	if err != nil {
		return fmt.Errorf("fd 3: %w", err)
	}
	unixAddr, ok := sa.(*syscall.SockaddrUnix)
	if !ok {
		return fmt.Errorf("fd 3 is bound to a %T", sa)
	}
	if fi, err := os.Stat(unixAddr.Name); err != nil || fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("fd 3's socket file %v: %w", fi, err)
	}
	return nil
}

// lines collects wb-vmd's log lines.
type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l.mu.Lock()
		l.all = append(l.all, sc.Text())
		l.mu.Unlock()
	}
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

// markRunnerDescriptors marks the test process's descriptors
// close-on-exec. The CI runner leaves descriptors open in the test
// process without close-on-exec (fd 131 on macos-26). Those are the
// runner's, not vmdspawn's, so they are marked here, before vmdspawn
// opens any: the check is that vmdspawn passes none of its own.
func markRunnerDescriptors() {
	for fd := 3; fd < 256; fd++ {
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
	}
}

// runDir returns a new run directory, mode 0700, whose socket paths fit
// sun_path.
func runDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "vs") //nolint:usetesting // t.TempDir's path holds the test name, which pushes the socket past sun_path's 104 bytes, and its subdirectory is mode 0755, which localipc refuses
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// config is the start of this test binary as wb-vmd for slot in dir.
func config(t *testing.T, dir, slot, version string, log *lines) vmdspawn.Config {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return vmdspawn.Config{
		Program: self, Slot: slot, RunDir: dir, Endpoint: "vmd-" + slot + "-1",
		Version: version, Log: log.read, Logger: slog.New(slog.DiscardHandler),
	}
}

func start(t *testing.T, version string) (*vmdspawn.Process, *lines, string) {
	t.Helper()
	markRunnerDescriptors()
	dir := runDir(t)
	log := &lines{}
	p, err := vmdspawn.Start(t.Context(), config(t, dir, "work", version, log))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, log, filepath.Join(dir, "vmd-work-1.sock")
}

// Both slots' wb-vmd started at the same time each get only their own
// descriptors 0, 2, 3 and 4: the fake checks its start interface from
// inside, so a descriptor of one spawn that leaked into the other's
// fails it (SEC08-proj-isolation).
func TestSpawnBothSlotsAtOnce(t *testing.T) {
	markRunnerDescriptors()
	dir := runDir(t)
	slots := []string{"work", "isolated"}
	procs := make([]*vmdspawn.Process, len(slots))
	logs := make([]*lines, len(slots))
	errs := make([]error, len(slots))
	var wg sync.WaitGroup
	for n, slot := range slots {
		logs[n] = &lines{}
		wg.Go(func() { procs[n], errs[n] = vmdspawn.Start(t.Context(), config(t, dir, slot, own, logs[n])) })
	}
	wg.Wait()
	for n, slot := range slots {
		if errs[n] != nil {
			t.Fatalf("start %s: %v", slot, errs[n])
		}
		t.Cleanup(func() { _ = procs[n].Close() })
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for n, slot := range slots {
		if _, err := procs[n].Client.GetState(ctx, &vmdv1.GetStateRequest{}); err != nil {
			t.Errorf("GetState of %s: %v; log: %s", slot, err, logs[n])
		}
		if !strings.Contains(logs[n].String(), `"slot":"`+slot+`"`) {
			t.Errorf("%s: no started record for the slot; log: %s", slot, logs[n])
		}
	}
}

// A file at the socket path, here a dangling symbolic link, is refused
// with ipc-endpoint-unsafe and left in place. Nothing is made at the
// link's target, and no wb-vmd starts.
func TestSocketPathTakenRefused(t *testing.T) {
	dir := runDir(t)
	sock := filepath.Join(dir, "vmd-work-1.sock")
	target := filepath.Join(dir, "elsewhere.sock")
	if err := os.Symlink(target, sock); err != nil {
		t.Fatal(err)
	}
	var buf syncBuffer
	cfg := config(t, dir, "work", own, &lines{})
	cfg.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	_, err := vmdspawn.Start(t.Context(), cfg)
	if r, ok := errors.AsType[*vmdspawn.UnsafeEndpointError](err); !ok || r.Type != os.ModeSymlink {
		t.Fatalf("err = %v, want an UnsafeEndpointError for a symbolic link", err)
	}
	if !strings.Contains(buf.String(), `"rule":"ipc-endpoint-unsafe"`) {
		t.Errorf("no ipc-endpoint-unsafe in the log: %s", buf.String())
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was made at the link's target: %v", err)
	}
	if fi, err := os.Lstat(sock); err != nil || fi.Mode().Type() != os.ModeSymlink {
		t.Errorf("the link isn't left in place: %v, %v", fi, err)
	}
	if strings.Contains(buf.String(), "wb-vmd started") {
		t.Errorf("wb-vmd started: %s", buf.String())
	}
}

// syncBuffer is a log sink for the decisions.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestSpawn starts the fake as wb-vmd, which checks from inside that it
// gets no arguments, the slot as its whole environment, and the
// descriptors 0, 2, 3 and 4 of its start interface and no others, with
// the gRPC socket's file mode 0600. A call through the version exchange
// reaches it. Closing the lifeline ends it with status 0, and the socket
// file is removed.
func TestSpawn(t *testing.T) {
	p, log, sock := start(t, own)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	resp, err := p.Client.GetState(ctx, &vmdv1.GetStateRequest{})
	if err != nil {
		t.Fatalf("GetState: %v; log: %s", err, log)
	}
	if resp.GetState().GetState() != vmdv1.State_STATE_STOPPED {
		t.Errorf("state %v", resp.GetState())
	}
	if !strings.Contains(log.String(), `"event":"started"`) {
		t.Errorf("no started record in the log: %s", log)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if st := p.ProcessState(); st == nil || !st.Success() {
		t.Fatalf("the fake ended %v; its log: %s", st, log)
	}
	if !strings.Contains(log.String(), `"event":"lifeline-closed"`) {
		t.Errorf("no lifeline-closed in the log: %s", log)
	}
	if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket file is still there: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

// A wb-vmd of another build version is refused by the version exchange:
// no call reaches it, and VersionRefusal names the rule.
func TestVersionMismatchRefused(t *testing.T) {
	p, log, _ := start(t, "9.9.9-dev+other")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := p.Client.GetState(ctx, &vmdv1.GetStateRequest{}); err == nil {
		t.Fatalf("GetState reached a wb-vmd of another version; log: %s", log)
	}
	if r := p.VersionRefusal(); r == nil {
		t.Fatal("no version refusal")
	}
}

// The exit of wb-vmd closes Exited.
func TestExited(t *testing.T) {
	p, _, _ := start(t, own)
	proc, err := os.FindProcess(p.Pid())
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Exited():
	case <-time.After(10 * time.Second):
		t.Fatal("Exited not closed")
	}
	if st := p.ProcessState(); st == nil || st.Success() {
		t.Errorf("state %v", st)
	}
}
