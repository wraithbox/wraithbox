package guestsock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestPort: wb-guestd's port is one only guest root can bind
// (X27-vsock-confinement, condition 1).
func TestPort(t *testing.T) {
	if Port >= 1024 {
		t.Fatalf("Port = %d, want below 1024", Port)
	}
	if Port != 1023 {
		t.Fatalf("Port = %d, want 1023, the highest port only root can bind", Port)
	}
}

// logCapture collects JSON log records.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) logger() *slog.Logger { return slog.New(slog.NewJSONHandler(c, nil)) }

func (c *logCapture) records(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for line := range bytes.Lines(c.buf.Bytes()) {
		rec := map[string]any{}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func (c *logCapture) withRule(t *testing.T, rule string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range c.records(t) {
		if rec["rule"] == rule {
			out = append(out, rec)
		}
	}
	return out
}

// fakeConn records whether it was closed.
type fakeConn struct {
	net.Conn
	mu     sync.Mutex
	closed bool
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

type acceptResult struct {
	conn net.Conn
	peer Peer
	err  error
}

// fakeSocket hands out queued accept results, then blocks until closed.
type fakeSocket struct {
	results chan acceptResult
	done    chan struct{}
	once    sync.Once
}

func newFakeSocket(results ...acceptResult) *fakeSocket {
	s := &fakeSocket{results: make(chan acceptResult, len(results)), done: make(chan struct{})}
	for _, r := range results {
		s.results <- r
	}
	return s
}

func (s *fakeSocket) accept() (net.Conn, Peer, error) {
	select {
	case r := <-s.results:
		return r.conn, r.peer, r.err
	case <-s.done:
		return nil, Peer{}, os.ErrClosed
	}
}

func (s *fakeSocket) close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}

func (s *fakeSocket) addr() net.Addr { return &net.UnixAddr{Name: "fake", Net: "unix"} }

// fakeClock is a clock the test moves.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func aborted() acceptResult {
	return acceptResult{err: os.NewSyscallError("accept", syscall.ECONNABORTED)}
}

func fromHost(c net.Conn) acceptResult {
	return acceptResult{conn: c, peer: Peer{CID: HostCID, Port: 50000}}
}

// TestAcceptSkipsErrors: failed accepts are logged and skipped, and the
// listener goes on to serve the host (X27-vsock-confinement, condition 3).
func TestAcceptSkipsErrors(t *testing.T) {
	good := &fakeConn{}
	sock := newFakeSocket(aborted(), aborted(),
		acceptResult{err: os.NewSyscallError("accept", syscall.EMFILE)},
		aborted(), fromHost(good))
	var logs logCapture
	l := newListener(sock, logs.logger(), time.Now)
	defer l.Close()

	got, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got != good {
		t.Fatalf("Accept returned %v, want the host's connection", got)
	}
	recs := logs.withRule(t, RuleAcceptFailed)
	if len(recs) != 1 {
		t.Fatalf("got %d %s records, want 1 in one window: %v", len(recs), RuleAcceptFailed, recs)
	}
	if recs[0]["decision"] != "skip" || recs[0]["errno"] != float64(syscall.ECONNABORTED) {
		t.Fatalf("record %v, want decision skip and errno ECONNABORTED", recs[0])
	}
}

// TestAcceptRefusesOtherPeers: a connection from any CID but the host's
// is closed and logged, and never returned (SEC08-proj-isolation).
func TestAcceptRefusesOtherPeers(t *testing.T) {
	self := &fakeConn{}
	hyper := &fakeConn{}
	good := &fakeConn{}
	sock := newFakeSocket(
		acceptResult{conn: self, peer: Peer{CID: 3, Port: 1023}},
		acceptResult{conn: hyper, peer: Peer{CID: 0, Port: 7}},
		fromHost(good))
	var logs logCapture
	l := newListener(sock, logs.logger(), time.Now)
	defer l.Close()

	got, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got != good {
		t.Fatalf("Accept returned %v, want the host's connection", got)
	}
	if !self.isClosed() || !hyper.isClosed() {
		t.Fatalf("refused connections left open: self %v, hypervisor %v", self.isClosed(), hyper.isClosed())
	}
	if good.isClosed() {
		t.Fatal("the host's connection was closed")
	}
	recs := logs.withRule(t, RulePeerNotHost)
	if len(recs) != 1 || recs[0]["peer_cid"] != float64(3) || recs[0]["decision"] != "close" {
		t.Fatalf("records %v, want one close of peer CID 3", recs)
	}
	served := 0
	for _, rec := range logs.records(t) {
		if rec["decision"] == "serve" && rec["peer_cid"] == float64(HostCID) {
			served++
		}
	}
	if served != 1 {
		t.Fatalf("%d serve records for the host's connection, want 1: %v", served, logs.records(t))
	}
}

// TestLogLimit: one record per kind per window, each with the count held
// back since the last one, and Close logs what is left (#115 item 1).
func TestLogLimit(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	var logs logCapture
	sock := newFakeSocket()
	l := newListener(sock, logs.logger(), clock.now)

	// Window 1: 5 errors, one logged with nothing held back before it.
	for range 5 {
		l.logAcceptError(syscall.ECONNABORTED)
		clock.advance(time.Second)
	}
	// Window 2 opens at 10 s: 7 errors, the first logged with the 4 held
	// back from window 1.
	clock.advance(5 * time.Second)
	for range 7 {
		l.logAcceptError(syscall.ECONNABORTED)
	}
	// A refused peer is its own kind, with its own window.
	l.logRefusedPeer(Peer{CID: 3})
	l.logRefusedPeer(Peer{CID: 3})
	_ = l.Close()

	held := heldBack(logs.withRule(t, RuleAcceptFailed))
	if want := []any{0.0, 4.0, 6.0}; !slices.Equal(held, want) {
		t.Fatalf("%s held_back counts %v, want %v (two windows, then Close)", RuleAcceptFailed, held, want)
	}
	held = heldBack(logs.withRule(t, RulePeerNotHost))
	if want := []any{0.0, 1.0}; !slices.Equal(held, want) {
		t.Fatalf("%s held_back counts %v, want %v", RulePeerNotHost, held, want)
	}
}

func heldBack(recs []map[string]any) []any {
	var out []any
	for _, r := range recs {
		out = append(out, r["held_back"])
	}
	return out
}

// TestCloseEndsAccept: Close wakes a waiting Accept with net.ErrClosed.
func TestCloseEndsAccept(t *testing.T) {
	l := newListener(newFakeSocket(), slog.New(slog.DiscardHandler), time.Now)
	accepted := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		accepted <- err
	}()
	time.Sleep(10 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-accepted; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close: %v, want net.ErrClosed", err)
	}
	if err := l.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("second Close: %v, want net.ErrClosed", err)
	}
}

// TestNextPause: an aborted accept is retried at once. Any other error
// backs off, doubling up to the cap.
func TestNextPause(t *testing.T) {
	if got := nextPause(time.Second, syscall.ECONNABORTED); got != 0 {
		t.Fatalf("after ECONNABORTED: %v, want 0", got)
	}
	var got []time.Duration
	p := time.Duration(0)
	for range 10 {
		p = nextPause(p, syscall.EMFILE)
		got = append(got, p)
	}
	if got[0] != firstAcceptPause || got[len(got)-1] != maxAcceptPause {
		t.Fatalf("pauses %v, want %v up to %v", got, firstAcceptPause, maxAcceptPause)
	}
}

// fakeBind returns the queued errors, then a fake socket.
type fakeBind struct {
	errs  []error
	calls int
}

func (b *fakeBind) bind(uint32) (socket, error) {
	b.calls++
	if len(b.errs) > 0 {
		err := b.errs[0]
		b.errs = b.errs[1:]
		return nil, err
	}
	return newFakeSocket(), nil
}

func inUse() error { return os.NewSyscallError("bind", syscall.EADDRINUSE) }

// TestListenRetriesInUse: EADDRINUSE is retried with a backoff from
// 100 ms doubling to 2 s, each try logged with its errno (#115 item 2).
func TestListenRetriesInUse(t *testing.T) {
	b := &fakeBind{}
	for range 8 {
		b.errs = append(b.errs, inUse())
	}
	var waits []time.Duration
	wait := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	var logs logCapture
	l, err := listen(t.Context(), logs.logger(), Port, b.bind, wait)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	ms := time.Millisecond
	want := []time.Duration{100 * ms, 200 * ms, 400 * ms, 800 * ms, 1600 * ms, 2000 * ms, 2000 * ms, 2000 * ms}
	if !slices.Equal(waits, want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
	recs := logs.withRule(t, RuleBindInUse)
	if len(recs) != 8 {
		t.Fatalf("got %d %s records, want 8", len(recs), RuleBindInUse)
	}
	if recs[0]["errno"] != float64(syscall.EADDRINUSE) || recs[0]["decision"] != "retry" {
		t.Fatalf("record %v, want errno EADDRINUSE and decision retry", recs[0])
	}
}

// TestListenFailsOnOtherErrors: EACCES, or any bind error but
// EADDRINUSE, is returned at once and logged with its errno: a broken
// install, not a race.
func TestListenFailsOnOtherErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EACCES, syscall.EADDRNOTAVAIL, syscall.EPERM} {
		t.Run(errno.Error(), func(t *testing.T) {
			b := &fakeBind{errs: []error{os.NewSyscallError("bind", errno)}}
			wait := func(context.Context, time.Duration) error {
				t.Fatal("waited after a fatal bind error")
				return nil
			}
			var logs logCapture
			l, err := listen(t.Context(), logs.logger(), Port, b.bind, wait)
			if !errors.Is(err, errno) || l != nil {
				t.Fatalf("listen = %v, %v; want nil, %v", l, err, errno)
			}
			if b.calls != 1 {
				t.Fatalf("bind called %d times, want 1", b.calls)
			}
			recs := logs.withRule(t, RuleBindFailed)
			if len(recs) != 1 || recs[0]["errno"] != float64(errno) || recs[0]["decision"] != "exit" {
				t.Fatalf("records %v, want one exit with errno %d", recs, errno)
			}
		})
	}
}

// TestListenStopsWithContext: the retry loop ends with its context.
func TestListenStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	b := &fakeBind{errs: []error{inUse(), inUse()}}
	wait := func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	l, err := listen(ctx, slog.New(slog.DiscardHandler), Port, b.bind, wait)
	if !errors.Is(err, context.Canceled) || l != nil {
		t.Fatalf("listen = %v, %v; want nil, context.Canceled", l, err)
	}
}
