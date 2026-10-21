package relay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// waitLimit bounds every wait of a test for the relay.
const waitLimit = 5 * time.Second

// events is the order of what the relay did to the fakes: writes to
// standard output and standard error, mode changes, and its own stop.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, s)
}

func (e *events) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.list...)
}

// text returns what was written to the output named name.
func (e *events) text(name string) string {
	var b strings.Builder
	for _, ev := range e.all() {
		if s, ok := strings.CutPrefix(ev, name+":"); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

// index returns the index of the first event with prefix and holding
// sub, or -1.
func (e *events) index(prefix, sub string) int {
	for i, ev := range e.all() {
		if strings.HasPrefix(ev, prefix) && strings.Contains(ev, sub) {
			return i
		}
	}
	return -1
}

// waitOut waits until standard output holds sub.
func (e *events) waitOut(t *testing.T, sub string) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !strings.Contains(e.text("stdout"), sub) {
		if time.Now().After(deadline) {
			t.Fatalf("standard output never got %q, got %q", sub, e.text("stdout"))
		}
		time.Sleep(time.Millisecond)
	}
}

// eventWriter records each write as an event. err, when set, fails
// every write.
type eventWriter struct {
	e    *events
	name string
	mu   sync.Mutex
	err  error
}

func (w *eventWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	err := w.err
	w.mu.Unlock()
	if err != nil {
		return 0, err
	}
	w.e.add(w.name + ":" + string(p))
	return len(p), nil
}

func (w *eventWriter) fail(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.err = err
}

// fakeTerminal records raw mode and its restore.
type fakeTerminal struct {
	e          *events
	mu         sync.Mutex
	rows, cols int
	panicSize  bool
}

func (f *fakeTerminal) setSize(rows, cols int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows, f.cols = rows, cols
}

func (f *fakeTerminal) MakeRaw() (func() error, error) {
	f.e.add("raw")
	return func() error {
		f.e.add("restore")
		return nil
	}, nil
}

func (f *fakeTerminal) Size() (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.panicSize {
		panic("size")
	}
	return f.rows, f.cols, nil
}

// fakeStream is wb-hostd's end of the stream: the test sends server
// frames with serve and reads client frames from sent.
type fakeStream struct {
	sent      chan *terminalv1.ClientFrame
	frames    chan received
	closed    chan struct{}
	closeOnce sync.Once
	sendErr   error
	panicRecv bool
}

func newFakeStream() *fakeStream {
	return &fakeStream{
		sent:   make(chan *terminalv1.ClientFrame, 64),
		frames: make(chan received, 64),
		closed: make(chan struct{}),
	}
}

func (s *fakeStream) Send(f *terminalv1.ClientFrame) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent <- f
	return nil
}

func (s *fakeStream) Recv() (*terminalv1.ServerFrame, error) {
	if s.panicRecv {
		panic("recv")
	}
	r, ok := <-s.frames
	if !ok {
		return nil, io.EOF
	}
	return r.frame, r.err
}

func (s *fakeStream) CloseSend() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *fakeStream) serve(frames ...*terminalv1.ServerFrame) {
	for _, f := range frames {
		s.frames <- received{frame: f}
	}
}

// next returns the next client frame.
func (s *fakeStream) next(t *testing.T) *terminalv1.ClientFrame {
	t.Helper()
	select {
	case f := <-s.sent:
		return f
	case <-time.After(waitLimit):
		t.Fatal("no client frame")
	}
	return nil
}

// none fails when a client frame arrives within d.
func (s *fakeStream) none(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case f := <-s.sent:
		t.Fatalf("unexpected client frame %v", f)
	case <-time.After(d):
	}
}

// harness runs one relay against fakes.
type harness struct {
	e      *events
	stream *fakeStream
	term   *fakeTerminal
	sigs   chan tty.Signal
	stdin  *io.PipeWriter
	stdout *eventWriter
	stderr *eventWriter
	logs   *syncBuffer
	cfg    Config
	done   chan int
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

var allTTYs = TTYs{Stdin: true, Stdout: true, Stderr: true}

// utf8Charset is a host terminal that reads UTF-8.
var utf8Charset = tty.Charset{Source: "LANG", Value: "en_US.UTF-8", UTF8: true}

func newHarness(t *testing.T, ttys TTYs) *harness {
	t.Helper()
	e := &events{}
	stdinR, stdinW := io.Pipe()
	h := &harness{
		e:      e,
		stream: newFakeStream(),
		term:   &fakeTerminal{e: e, rows: 24, cols: 80},
		sigs:   make(chan tty.Signal, 8),
		stdin:  stdinW,
		stdout: &eventWriter{e: e, name: "stdout"},
		stderr: &eventWriter{e: e, name: "stderr"},
		logs:   &syncBuffer{},
		done:   make(chan int, 1),
	}
	h.cfg = Config{
		Stream:       h.stream,
		Terminal:     h.term,
		Charset:      utf8Charset,
		Signals:      h.sigs,
		Stdin:        stdinR,
		Stdout:       h.stdout,
		Stderr:       h.stderr,
		TTY:          ttys,
		ID:           "myrepo-20261021-1412-k3f9",
		Log:          slog.New(slog.NewTextHandler(h.logs, nil)),
		DrainTimeout: 20 * time.Millisecond,
	}
	h.cfg.StopSelf = func() error {
		e.add("stop-self")
		h.sigs <- tty.Continue
		return nil
	}
	t.Cleanup(func() { _ = stdinW.Close() })
	return h
}

func (h *harness) start() {
	go func() { h.done <- Run(h.cfg) }()
}

// wait returns Run's exit status.
func (h *harness) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-h.done:
		return code
	case <-time.After(waitLimit):
		t.Fatal("Run didn't return")
	}
	return 0
}

// typeIn writes p to standard input without blocking the test.
func (h *harness) typeIn(p string) {
	go func() { _, _ = h.stdin.Write([]byte(p)) }()
}

// lastLine returns the last line written to standard error.
func (h *harness) lastLine() string {
	lines := strings.Split(strings.TrimRight(h.e.text("stderr"), "\n"), "\n")
	return lines[len(lines)-1]
}

func outFrame(stream terminalv1.Stream, data string) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Output{Output: &terminalv1.Output{Stream: stream, Data: []byte(data)}}}
}

func stdout(data string) *terminalv1.ServerFrame {
	return outFrame(terminalv1.Stream_STREAM_STDOUT, data)
}

func exitCode(n uint32) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: n}}}}
}

func exitSignal(n uint32) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Signal{Signal: n}}}}
}

func failureFrame(rule, msg string) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Failure{Failure: &terminalv1.Failure{Rule: rule, Message: msg}}}
}

func stopped() *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Stopped{Stopped: &terminalv1.Stopped{}}}
}

// kind names a client frame's oneof member, for comparisons.
func kind(f *terminalv1.ClientFrame) string {
	switch v := f.GetFrame().(type) {
	case *terminalv1.ClientFrame_Input:
		return "input " + string(v.Input)
	case *terminalv1.ClientFrame_Resize:
		return fmt.Sprintf("resize %dx%d", v.Resize.GetRows(), v.Resize.GetCols())
	case *terminalv1.ClientFrame_Signal:
		return "signal " + v.Signal.String()
	case *terminalv1.ClientFrame_Stop:
		return "stop"
	case *terminalv1.ClientFrame_Resume:
		return "resume"
	case *terminalv1.ClientFrame_StdinClosed:
		return "stdin-closed"
	case *terminalv1.ClientFrame_StdoutClosed:
		return "stdout-closed"
	case *terminalv1.ClientFrame_Hangup:
		return "hangup"
	}
	return "none"
}

var errBroken = errors.New("connection reset")
