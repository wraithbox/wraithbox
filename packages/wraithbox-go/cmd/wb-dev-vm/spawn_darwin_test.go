package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// childResult is the line the stand-in for wb-vmd logs when its start
// interface is as S04-03-self-sandboxing, "How `wb-vmd` is started",
// gives it.
const childResult = "start interface ok"

// TestMain runs the stand-in for wb-vmd when the test binary is started
// as wb-dev-vm starts wb-vmd: its whole environment is the slot.
func TestMain(m *testing.M) {
	if slices.Equal(os.Environ(), []string{slotEnv}) {
		os.Exit(standIn())
	}
	os.Exit(m.Run())
}

// standIn checks the start interface, logs a started record and the
// result on fd 2, and exits when the lifeline on fd 0 closes.
func standIn() int {
	log := os.NewFile(2, "log")
	if err := checkStartInterface(); err != nil {
		fmt.Fprintf(log, `{"event":"refused","detail":%q}`+"\n", err.Error())
		return 1
	}
	fmt.Fprintf(log, `{"event":"started","version":"stand-in"}`+"\n")
	fmt.Fprintf(log, `{"event":%q}`+"\n", childResult)
	_, _ = io.Copy(io.Discard, os.Stdin)
	return 0
}

func checkStartInterface() error {
	if len(os.Args) != 1 {
		return fmt.Errorf("arguments %q", os.Args[1:])
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

// syncBuffer is wb-vmd's log as wb-dev-vm prints it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
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

// TestSpawn starts the stand-in as wb-vmd and checks, from inside it,
// that it gets no arguments, the slot as its whole environment, and the
// descriptors 0, 2, 3 and 4 of its start interface and no others, with
// the gRPC socket's file mode 0600. Closing the lifeline ends it.
func TestSpawn(t *testing.T) {
	dir := t.TempDir()
	d, err := makeDescriptors(dir)
	if err != nil {
		t.Fatal(err)
	}
	log := &syncBuffer{}
	s := &session{log: slog.New(slog.DiscardHandler), dir: dir, exited: make(chan struct{}), life: d.lifeWrite}
	started := make(chan string, 1)
	// The CI runner leaves descriptors open in the test process without
	// close-on-exec (fd 131 on macos-26). Those are the runner's, not
	// wb-dev-vm's, so they are marked here: the check is that wb-dev-vm
	// passes none of its own.
	for fd := 3; fd < 256; fd++ {
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.spawn(context.Background(), self, d, log, started); err != nil {
		d.closeAll()
		t.Fatal(err)
	}
	defer func() { _ = d.handoffOwn.Close() }()
	select {
	case v := <-started:
		if v != "stand-in" {
			t.Fatalf("started version %q", v)
		}
	case <-s.exited:
		t.Fatalf("the stand-in exited: %s", log.String())
	case <-time.After(10 * time.Second):
		t.Fatalf("no started record: %s", log.String())
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if !s.cmd.ProcessState.Success() || !strings.Contains(log.String(), childResult) {
		t.Fatalf("the stand-in ended %s; its log: %s", s.cmd.ProcessState, log.String())
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the run directory is still there: %v", err)
	}
}
