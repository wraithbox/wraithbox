package shells

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// fakeGuest is wb-guestd's side of the stream: the test feeds frames,
// and Cancel makes Recv fail.
type fakeGuest struct {
	frames    chan guestRecv
	sent      chan *terminalv1.ClientFrame
	cancelled chan struct{}
	once      sync.Once
}

func (g *fakeGuest) Recv() (*guestv1.ShellResponse, error) {
	select {
	case r := <-g.frames:
		return r.resp, r.err
	case <-g.cancelled:
		return nil, status.Error(codes.Canceled, "context canceled")
	}
}

func (g *fakeGuest) Send(req *guestv1.ShellRequest) error {
	g.sent <- req.GetFrame()
	return nil
}

func (g *fakeGuest) cancel() { g.once.Do(func() { close(g.cancelled) }) }

func (g *fakeGuest) frame(f *terminalv1.ServerFrame) {
	g.frames <- guestRecv{resp: &guestv1.ShellResponse{Frame: f}}
}

func (g *fakeGuest) end(err error) { g.frames <- guestRecv{err: err} }

// fakeClient is wb's side: the test feeds requests, and closing reqs
// ends wb's stream.
type fakeClient struct {
	reqs chan *hostdv1.ShellRequest
	sent chan *terminalv1.ServerFrame
}

func (c *fakeClient) Context() context.Context { return context.Background() }

func (c *fakeClient) Recv() (*hostdv1.ShellRequest, error) {
	req, ok := <-c.reqs
	if !ok {
		return nil, io.EOF
	}
	return req, nil
}

func (c *fakeClient) Send(resp *hostdv1.ShellResponse) error {
	c.sent <- resp.GetFrame()
	return nil
}

func (c *fakeClient) frame(f *terminalv1.ClientFrame) {
	c.reqs <- &hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Frame{Frame: f}}
}

// fakeAfter fires every wait at once but HangupWait, which the test
// fires, and records each wait asked for.
type fakeAfter struct {
	mu     sync.Mutex
	asked  []time.Duration
	hangup chan time.Time
}

func (a *fakeAfter) after(d time.Duration) <-chan time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, d)
	if d == HangupWait {
		return a.hangup
	}
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

func (a *fakeAfter) waits() []time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]time.Duration(nil), a.asked...)
}

type relayTest struct {
	guest  *fakeGuest
	client *fakeClient
	after  *fakeAfter
	down   chan struct{}
	logs   *syncBuffer
	out    chan Outcome
}

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

// startRelay runs a relay of a bound shell with setup.
func startRelay(t *testing.T, setup *terminalv1.Setup) *relayTest {
	t.Helper()
	g := NewRegistry(&fakeKiller{}, nil)
	t.Cleanup(g.Close)
	rt := &relayTest{
		guest: &fakeGuest{
			frames: make(chan guestRecv), sent: make(chan *terminalv1.ClientFrame, 16),
			cancelled: make(chan struct{}),
		},
		client: &fakeClient{reqs: make(chan *hostdv1.ShellRequest), sent: make(chan *terminalv1.ServerFrame, 16)},
		after:  &fakeAfter{hangup: make(chan time.Time, 1)},
		down:   make(chan struct{}),
		logs:   &syncBuffer{},
		out:    make(chan Outcome, 1),
	}
	r := &Relay{
		Shell: open(t, g, "a", "p"), Client: rt.client, Guest: rt.guest, Cancel: rt.guest.cancel,
		Down: rt.down, Checker: NewChecker(setup), Log: slog.New(slog.NewJSONHandler(rt.logs, nil)),
		After: rt.after.after,
	}
	go func() { rt.out <- r.Run() }()
	return rt
}

func (rt *relayTest) outcome(t *testing.T) Outcome {
	t.Helper()
	select {
	case o := <-rt.out:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the relay didn't end")
		return Outcome{}
	}
}

// toClient returns the next frame wb got.
func (rt *relayTest) toClient(t *testing.T) *terminalv1.ServerFrame {
	t.Helper()
	select {
	case f := <-rt.client.sent:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("wb got no frame")
		return nil
	}
}

// toGuest returns the next frame wb-guestd got.
func (rt *relayTest) toGuest(t *testing.T) *terminalv1.ClientFrame {
	t.Helper()
	select {
	case f := <-rt.guest.sent:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("wb-guestd got no frame")
		return nil
	}
}

func (rt *relayTest) noMoreToClient(t *testing.T) {
	t.Helper()
	select {
	case f := <-rt.client.sent:
		t.Fatalf("wb got another frame: %v", f)
	default:
	}
}

func TestRelayExit(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.guest.frame(outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("hi")))
	rt.guest.frame(exitCode(3))
	rt.guest.end(io.EOF)
	o := rt.outcome(t)
	if o.Exit.GetCode() != 3 || o.Lost {
		t.Fatalf("Outcome = %+v", o)
	}
	if got := rt.toClient(t).GetOutput().GetData(); string(got) != "hi" {
		t.Fatalf("output = %q", got)
	}
	if got := rt.toClient(t).GetExit().GetCode(); got != 3 {
		t.Fatalf("exit = %d", got)
	}
	if !strings.Contains(rt.logs.String(), `"rule":"shell-exit"`) {
		t.Fatalf("log = %s", rt.logs.String())
	}
}

// TestRelayFailures feeds the relay each frame out of bounds, and sees
// wb get a failure with its rule, the guest a hangup, and the rule
// logged.
func TestRelayFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame *terminalv1.ServerFrame
		rule  string
	}{
		{"exit 256", exitCode(256), RuleExitStatus},
		{"signal 0", exitSignal(0), RuleExitStatus},
		{"signal 65", exitSignal(65), RuleExitStatus},
		{"stderr not asked for", outFrame(terminalv1.Stream_STREAM_STDERR, []byte("x")), RuleFrame},
		{"output over the cap", outFrame(terminalv1.Stream_STREAM_STDOUT, make([]byte, MaxFrameData+1)), RuleFrame},
		{"unset", &terminalv1.ServerFrame{}, RuleFrame},
		{"guest failure", failFrame("guest-pty", "\x1b[2Jboom"), RuleFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := startRelay(t, &terminalv1.Setup{})
			rt.guest.frame(tc.frame)
			f := rt.toClient(t).GetFailure()
			if f.GetRule() != tc.rule || strings.ContainsRune(f.GetMessage(), 0x1b) {
				t.Fatalf("failure = %+v", f)
			}
			if rt.toGuest(t).GetHangup() == nil {
				t.Fatal("the guest got no hangup")
			}
			rt.guest.end(io.EOF)
			if o := rt.outcome(t); o.Rule != tc.rule || o.Exit != nil {
				t.Fatalf("Outcome = %+v", o)
			}
			if !strings.Contains(rt.logs.String(), `"rule":"`+tc.rule+`"`) {
				t.Fatalf("log = %s", rt.logs.String())
			}
		})
	}
}

// TestRelayAfterExit sees a frame after the exit dropped and logged.
func TestRelayAfterExit(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.guest.frame(exitCode(0))
	rt.guest.frame(outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("late")))
	rt.guest.end(io.EOF)
	if o := rt.outcome(t); o.Exit.GetCode() != 0 || o.Exit == nil {
		t.Fatalf("Outcome = %+v", o)
	}
	if rt.toClient(t).GetExit() == nil {
		t.Fatal("wb didn't get the exit")
	}
	rt.noMoreToClient(t)
	if !strings.Contains(rt.logs.String(), "guest frame after the last one") {
		t.Fatalf("log = %s", rt.logs.String())
	}
}

// TestRelayStoppedRepeat sees a repeated stopped frame dropped, counted
// and logged.
func TestRelayStoppedRepeat(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{Interactive: true, Size: &terminalv1.WindowSize{Rows: 24, Cols: 80}})
	rt.guest.frame(stoppedFrame())
	rt.guest.frame(stoppedFrame())
	rt.guest.frame(exitCode(0))
	rt.guest.end(io.EOF)
	rt.outcome(t)
	if rt.toClient(t).GetStopped() == nil || rt.toClient(t).GetExit() == nil {
		t.Fatal("wb didn't get the stopped frame and the exit")
	}
	if !strings.Contains(rt.logs.String(), `"rule":"guest-stopped-repeat"`) || !strings.Contains(rt.logs.String(), `"count":1`) {
		t.Fatalf("log = %s", rt.logs.String())
	}
}

// TestRelayClientFrames sees wb's frames passed to the guest.
func TestRelayClientFrames(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.client.frame(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Input{Input: []byte("ls\n")}})
	if got := rt.toGuest(t).GetInput(); string(got) != "ls\n" {
		t.Fatalf("input = %q", got)
	}
	rt.guest.frame(exitCode(0))
	rt.guest.end(io.EOF)
	rt.outcome(t)
}

// TestRelayClientClose sees a closed wb stream send the guest a hangup,
// and the shell still end by its exit.
func TestRelayClientClose(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	close(rt.client.reqs)
	if rt.toGuest(t).GetHangup() == nil {
		t.Fatal("the guest got no hangup")
	}
	rt.guest.frame(exitSignal(1))
	rt.guest.end(io.EOF)
	if o := rt.outcome(t); o.Exit.GetSignal() != 1 {
		t.Fatalf("Outcome = %+v", o)
	}
	if !strings.Contains(rt.logs.String(), `"rule":"shell-hangup"`) {
		t.Fatalf("log = %s", rt.logs.String())
	}
}

// TestRelayClientNotFrame sees a second open from wb taken as a hangup.
func TestRelayClientNotFrame(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.client.reqs <- &hostdv1.ShellRequest{Msg: &hostdv1.ShellRequest_Open{Open: &hostdv1.ShellOpen{}}}
	if rt.toGuest(t).GetHangup() == nil {
		t.Fatal("the guest got no hangup")
	}
	rt.guest.frame(exitCode(0))
	rt.guest.end(io.EOF)
	rt.outcome(t)
	if !strings.Contains(rt.logs.String(), `"rule":"shell-client-frame"`) {
		t.Fatalf("log = %s", rt.logs.String())
	}
}

// TestRelayHangupTimeout sees a guest stream that doesn't end within
// HangupWait of the hangup cancelled, and never before the hangup.
func TestRelayHangupTimeout(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	close(rt.client.reqs)
	if rt.toGuest(t).GetHangup() == nil {
		t.Fatal("the guest got no hangup")
	}
	rt.after.hangup <- time.Time{}
	if o := rt.outcome(t); o.Rule != RuleHangupTimeout {
		t.Fatalf("Outcome = %+v", o)
	}
	if f := rt.toClient(t).GetFailure(); f.GetRule() != RuleHangupTimeout {
		t.Fatalf("failure = %+v", f)
	}
	select {
	case <-rt.guest.cancelled:
	default:
		t.Fatal("the guest stream wasn't cancelled")
	}
}

// TestRelayIdleClient sees an idle wb keep its shell: the relay arms no
// wait and sends no hangup while neither side sends, however long that
// is (S06-09-session-lifecycle, "Closing the terminal ends the
// session").
func TestRelayIdleClient(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.guest.frame(outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("x")))
	rt.toClient(t)
	if w := rt.after.waits(); len(w) != 0 {
		t.Fatalf("waits armed while idle: %v", w)
	}
	select {
	case f := <-rt.guest.sent:
		t.Fatalf("the guest got %v while wb was idle", f)
	default:
	}
	rt.guest.frame(exitCode(0))
	rt.guest.end(io.EOF)
	if o := rt.outcome(t); o.Exit == nil {
		t.Fatalf("Outcome = %+v", o)
	}
}

// TestRelayLost sees a stream that fails as the link drops end the
// shell as lost.
func TestRelayLost(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.guest.frame(outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("x")))
	close(rt.down)
	rt.guest.end(status.Error(codes.Unavailable, "transport is closing"))
	if o := rt.outcome(t); !o.Lost || o.Rule != RuleLost {
		t.Fatalf("Outcome = %+v", o)
	}
	rt.toClient(t)
	if f := rt.toClient(t).GetFailure(); f.GetRule() != RuleLost {
		t.Fatalf("failure = %+v", f)
	}
}

// TestRelayUnavailableNotLost sees a transport failure with the link up
// taken as the stream's own end.
func TestRelayUnavailableNotLost(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.guest.frame(outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("x")))
	rt.guest.end(status.Error(codes.Unavailable, "transport is closing"))
	if o := rt.outcome(t); o.Lost || o.Rule != RuleClosed {
		t.Fatalf("Outcome = %+v", o)
	}
}

// TestRelayRefused sees a guest status before any frame refuse the
// shell, with the guest's reason in the log only, escaped.
func TestRelayRefused(t *testing.T) {
	rt := startRelay(t, &terminalv1.Setup{})
	rt.guest.end(status.Error(codes.PermissionDenied, "\x1b[31mno such user"))
	if o := rt.outcome(t); o.Rule != RuleRefused || o.Lost {
		t.Fatalf("Outcome = %+v", o)
	}
	f := rt.toClient(t).GetFailure()
	if f.GetRule() != RuleRefused || strings.Contains(f.GetMessage(), "no such user") {
		t.Fatalf("failure = %+v", f)
	}
	if logs := rt.logs.String(); !strings.Contains(logs, `no such user`) || strings.Contains(logs, "\\u001b[31m") {
		t.Fatalf("log = %s", logs)
	}
}
