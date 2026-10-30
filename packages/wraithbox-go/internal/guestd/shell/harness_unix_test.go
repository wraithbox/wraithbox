//go:build darwin || linux

package shell

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

const testShellID = "proj-20261030-1200-ab12"

// lockedBuffer is a log sink the shell's goroutines share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeUsers knows one user.
type fakeUsers struct {
	u          users.User
	privileged bool
	dirErr     error
	holdErr    error
}

func (f *fakeUsers) KnownByName(name string) (users.User, bool) { return f.u, name == f.u.Name }

func (f *fakeUsers) Privileged(context.Context, users.User) (bool, error) {
	return f.privileged, f.dirErr
}

func (f *fakeUsers) Hold(_ users.User, start func()) error {
	if f.holdErr != nil {
		return f.holdErr
	}
	start()
	return nil
}

// fakeStopper records what the shell forgets.
type fakeStopper struct {
	mu     sync.Mutex
	forgot []gp.Key
}

func (*fakeStopper) Idle() {}

func (f *fakeStopper) Forget(keys []gp.Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgot = append(f.forgot, keys...)
}

// fakeStream is the host's end of a Shell call.
type fakeStream struct {
	ctx context.Context
	in  chan *guestv1.ShellRequest
	out chan *terminalv1.ServerFrame
}

func (f *fakeStream) Context() context.Context { return f.ctx }

func (f *fakeStream) Send(r *guestv1.ShellResponse) error {
	select {
	case f.out <- r.GetFrame():
		return nil
	case <-f.ctx.Done():
		return f.ctx.Err()
	}
}

func (f *fakeStream) Recv() (*guestv1.ShellRequest, error) {
	select {
	case r, ok := <-f.in:
		if !ok {
			return nil, io.EOF
		}
		return r, nil
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
}

// harness runs one shell of the test program.
type harness struct {
	t      *testing.T
	srv    *Server
	users  *fakeUsers
	stop   *fakeStopper
	log    *lockedBuffer
	cancel context.CancelFunc
	st     *fakeStream
	done   chan error
	frames []*terminalv1.ServerFrame
}

func testUser(t *testing.T) users.User {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("the shell refuses root, and the tests run as the user")
	}
	return users.User{
		ProjectID: "p1", Name: "wb-test", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Home: t.TempDir(),
	}
}

// newHarness returns a Server for the test program in mode.
func newHarness(t *testing.T, mode string) *harness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, users: &fakeUsers{u: testUser(t)}, stop: &fakeStopper{}, log: &lockedBuffer{}}
	h.srv = New(Config{
		Log:        slog.New(slog.NewJSONHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Users:      h.users,
		Table:      sessionproc.System{},
		Stopper:    h.stop,
		MountCheck: func(context.Context) string { return "" },
		Dropped:    func(context.Context) bool { return false },
		Helper:     []string{exe, testHelperArg, mode},
		Credential: func(users.User) *sessionproc.Cred { return nil },
		KillDelay:  500 * time.Millisecond,
	})
	t.Cleanup(func() { t.Logf("guestd log:\n%s", h.log) })
	return h
}

func interactive(rows, cols uint32) *terminalv1.Setup {
	return &terminalv1.Setup{Interactive: true, Size: &terminalv1.WindowSize{Rows: rows, Cols: cols}}
}

// start sends the start, and runs the shell.
func (h *harness) start(setup *terminalv1.Setup) {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.t.Cleanup(cancel)
	h.st = &fakeStream{ctx: ctx, in: make(chan *guestv1.ShellRequest, 16), out: make(chan *terminalv1.ServerFrame, 1024)}
	h.done = make(chan error, 1)
	h.st.in <- &guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Start{Start: &guestv1.ShellStart{
		ShellId: testShellID, User: h.users.u.Name, Setup: setup,
	}}}
	go func() { h.done <- h.srv.Shell(h.st) }()
}

func (h *harness) send(f *terminalv1.ClientFrame) {
	h.st.in <- &guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Frame{Frame: f}}
}

// output returns the output frames so far of stream, joined.
func (h *harness) output(stream terminalv1.Stream) string {
	var b strings.Builder
	for _, f := range h.frames {
		if o := f.GetOutput(); o != nil && o.GetStream() == stream {
			b.Write(o.GetData())
		}
	}
	return b.String()
}

// waitFor takes frames until cond holds.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.After(15 * time.Second)
	for !cond() {
		select {
		case f := <-h.st.out:
			h.frames = append(h.frames, f)
		case err := <-h.done:
			h.done <- err
			h.drainFrames()
			if !cond() {
				h.t.Fatalf("the shell ended (%v) before %s; frames %v", err, what, h.frames)
			}
			return
		case <-deadline:
			h.t.Fatalf("no %s; frames %v", what, h.frames)
		}
	}
}

// waitOutput waits for s on standard output.
func (h *harness) waitOutput(s string) {
	h.t.Helper()
	h.waitFor("output "+s, func() bool { return strings.Contains(h.output(stdout), s) })
}

func (h *harness) drainFrames() {
	for {
		select {
		case f := <-h.st.out:
			h.frames = append(h.frames, f)
		default:
			return
		}
	}
}

// end waits for Shell to return, and returns its last frame.
func (h *harness) end() *terminalv1.ServerFrame {
	h.t.Helper()
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatalf("Shell: %v", err)
		}
	case <-time.After(20 * time.Second):
		h.t.Fatalf("the shell didn't end; frames %v", h.frames)
	}
	h.drainFrames()
	if len(h.frames) == 0 {
		h.t.Fatal("no frames")
	}
	return h.frames[len(h.frames)-1]
}

// count returns how many frames match.
func (h *harness) count(match func(*terminalv1.ServerFrame) bool) int {
	n := 0
	for _, f := range h.frames {
		if match(f) {
			n++
		}
	}
	return n
}

func wantExitCode(t *testing.T, f *terminalv1.ServerFrame, code uint32) {
	t.Helper()
	e, ok := f.GetExit().GetStatus().(*terminalv1.Exit_Code)
	if !ok || e.Code != code {
		t.Fatalf("last frame %v, want exit code %d", f, code)
	}
}

func wantExitSignal(t *testing.T, f *terminalv1.ServerFrame, sig uint32) {
	t.Helper()
	e, ok := f.GetExit().GetStatus().(*terminalv1.Exit_Signal)
	if !ok || e.Signal != sig {
		t.Fatalf("last frame %v, want exit signal %d", f, sig)
	}
}
