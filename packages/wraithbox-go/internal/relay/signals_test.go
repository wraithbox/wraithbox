//go:build darwin || linux

package relay

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// childEnv makes the test binary run the relay of TestSignalsOnPTY.
const childEnv = "WB_RELAY_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) != "" {
		os.Exit(runChild())
	}
	os.Exit(m.Run())
}

// runChild runs the relay with System's wiring, on the PTY the parent
// gave it, against scriptedStream. It writes each client frame as a line
// to descriptor 3.
func runChild() int {
	frames := os.NewFile(3, "frames")
	cfg, stop, err := System()
	if err != nil {
		_, _ = fmt.Fprintln(frames, "system:", err)
		return 99
	}
	defer stop()
	s := &scriptedStream{log: frames, frames: make(chan *terminalv1.ServerFrame, 8)}
	s.frames <- stdout("ready\r\n")
	cfg.Stream = s
	cfg.ID = "child"
	return Run(cfg)
}

// scriptedStream is a server that answers a stop with a stopped frame,
// and a hangup with output and the exit code 3.
type scriptedStream struct {
	log    io.Writer
	frames chan *terminalv1.ServerFrame
}

func (s *scriptedStream) Send(f *terminalv1.ClientFrame) error {
	_, _ = fmt.Fprintln(s.log, kind(f))
	switch {
	case f.GetStop() != nil:
		s.frames <- stopped()
	case f.GetHangup() != nil:
		s.frames <- stdout("after the hangup")
		s.frames <- exitCode(3)
	}
	return nil
}

func (s *scriptedStream) Recv() (*terminalv1.ServerFrame, error) { return <-s.frames, nil }

func (s *scriptedStream) CloseSend() error { return nil }

// TestSignalsOnPTY runs the relay in a child process under a real PTY,
// as the PTY's session leader, and sends it real SIGWINCH, SIGTSTP,
// SIGCONT and SIGHUP, so the platform wiring of
// S05-02-exit-status-and-signals is tested and not only the fakes.
func TestSignalsOnPTY(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	logR, logW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.ExtraFiles = []*os.File{logW}
	ptm, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	_ = logW.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptm.Close() }()
	out := &syncBuffer{}
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(out, ptm); close(copied) }()
	lines := frameLines(logR)

	waitText(t, out, "ready", 1)
	if err := pty.Setsize(ptm, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	expectLine(t, lines, "resize 30x100")

	signal(t, cmd, unix.SIGTSTP)
	expectLine(t, lines, "stop")
	waitStopped(t, cmd.Process.Pid)
	if attrs := termios(t, ptm); attrs.Lflag&unix.ICANON == 0 {
		t.Errorf("stopped wb left the terminal in raw mode: lflag %#x", attrs.Lflag)
	}

	signal(t, cmd, unix.SIGCONT)
	expectLine(t, lines, "resize 30x100")
	expectLine(t, lines, "resume")
	if attrs := termios(t, ptm); attrs.Lflag&(unix.ICANON|unix.ISIG) != 0 {
		t.Errorf("after SIGCONT the terminal isn't raw: lflag %#x", attrs.Lflag)
	}
	waitText(t, out, "wb: child", 2)

	mark := len(out.String())
	signal(t, cmd, unix.SIGHUP)
	expectLine(t, lines, "hangup")
	err = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 3 {
		t.Errorf("exit status %d (%v), want 3", code, err)
	}
	select {
	case <-copied:
	case <-time.After(waitLimit):
		t.Fatal("the PTY didn't close")
	}
	if after := out.String()[mark:]; after != "" {
		t.Errorf("after SIGHUP the terminal got %q", after)
	}
}

func frameLines(r io.Reader) <-chan string {
	lines := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	return lines
}

func expectLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	select {
	case got := <-lines:
		if got != want {
			t.Fatalf("frame %q, want %q", got, want)
		}
	case <-time.After(waitLimit):
		t.Fatalf("no frame, want %q", want)
	}
}

func waitText(t *testing.T, out *syncBuffer, sub string, n int) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for strings.Count(out.String(), sub) < n {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never got %q %d times: %q", sub, n, out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func signal(t *testing.T, cmd *exec.Cmd, s os.Signal) {
	t.Helper()
	if err := cmd.Process.Signal(s); err != nil {
		t.Fatal(err)
	}
}

// waitStopped waits until the process stopped, without reaping it.
func waitStopped(t *testing.T, pid int) {
	t.Helper()
	var ws unix.WaitStatus
	if _, err := unix.Wait4(pid, &ws, unix.WUNTRACED, nil); err != nil {
		t.Fatal(err)
	}
	// The low byte 0x7f is a stopped child on darwin and linux. darwin's
	// WaitStatus.Stopped leaves out a stop by SIGSTOP, which this is.
	if ws&0xff != 0x7f || unix.Signal(ws>>8&0xff) != unix.SIGSTOP {
		t.Fatalf("wait status %#x, want stopped by SIGSTOP", uint32(ws))
	}
}
