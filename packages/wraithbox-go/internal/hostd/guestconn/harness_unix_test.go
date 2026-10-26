//go:build unix

package guestconn

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk/datadisktest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

// own is wb-hostd's version in these tests, set explicitly rather than
// taken from the test binary's stamp (S10-02-packaging-and-signing, "One
// build version").
const own = "1.2.3-dev+abc"

// fakeClock moves only when a test advances it. Each After call is
// reported on waits, so a test sees every wait the link asks for.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	waits  chan time.Duration
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_000_000, 0), waits: make(chan time.Duration, 4096)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := fakeTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	c.waits <- d
	return t.ch
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, t := range c.timers {
		if t.at.After(c.now) {
			kept = append(kept, t)
			continue
		}
		t.ch <- c.now
	}
	c.timers = kept
}

// nextWait returns the next wait the link asks for.
func (c *fakeClock) nextWait(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-c.waits:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("the link asked for no wait")
		return 0
	}
}

// skipTo skips the waits asked for until one of want, then advances by
// it.
func (c *fakeClock) skipTo(t *testing.T, want time.Duration) {
	t.Helper()
	d := c.nextWait(t)
	for d != want {
		d = c.nextWait(t)
	}
	c.advance(want)
}

// liveCount is the number of live connections.
func (vc *vmConns) liveCount() int {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return len(vc.live)
}

// logs collects JSON log records.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) records(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for line := range bytes.Lines(l.buf.Bytes()) {
		rec := map[string]any{}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// withRule returns the records of rule.
func (l *logs) withRule(t *testing.T, rule string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range l.records(t) {
		if rec["rule"] == rule {
			out = append(out, rec)
		}
	}
	return out
}

// harness is a VM manager with the fake provider, whose guest ends are
// each served by wb-guestd's GuestService over a socketpair, and Links
// that follow its runs.
type harness struct {
	t        *testing.T
	provider *vmtest.Provider
	manager  *vm.Manager
	links    *Links
	clock    *fakeClock
	logs     *logs
	// guestVersion is the version the guest's wb-guestd reports.
	guestVersion string
	// guests and hosts receive the two ends of each connection, in
	// order.
	guests chan net.Conn
	hosts  chan net.Conn
	// times receives the generation of each time hook call, and reasons
	// its reason.
	times   chan uint64
	reasons chan guestv1.TimeReason
	// send, when set, is what the time hook does after it records the
	// call, and guestClock the clock of the guests' wb-guestd. Set them
	// before the first start.
	send       func(ctx context.Context, l *guestlink.Link, reason guestv1.TimeReason) error
	guestClock guestd.Clock
	// mount, when set, is the mount hook. nil mounts at once. guestMount,
	// when set, is the data mount of the guests' wb-guestd. Set them
	// before the first start.
	mount      MountHook
	guestMount *guestd.DataMount
	// users, when set, serves each connection instead of wb-guestd's
	// GuestService. Set it before the first start.
	users *usersGuest
	// db is the state.db of the VM manager and the links, and cfg the
	// links' configuration.
	db  *state.DB
	cfg Config
}

// newHarness returns a harness whose wb-hostd has version host and whose
// guests report guest. With follow, the links follow every run of the
// manager. Without, a test opens connections itself.
func newHarness(t *testing.T, host, guest string, follow bool) *harness {
	t.Helper()
	h := &harness{
		t: t, provider: &vmtest.Provider{}, clock: newFakeClock(), logs: &logs{},
		guestVersion: guest, guests: make(chan net.Conn, 1024), hosts: make(chan net.Conn, 1024), times: make(chan uint64, 1024),
		reasons: make(chan guestv1.TimeReason, 1024),
	}
	h.provider.OnConnect = h.connect
	logger := slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	vmCfg := h.vmConfig(logger)
	cfg := Config{Version: host, Clock: h.clock, Logger: logger, TimeHook: h.timeHook, MountHook: h.mountHook, Users: h.db}
	if follow {
		vmCfg.OnRunning = func(slot vm.Slot, generation uint64, ended <-chan struct{}) {
			h.links.Run(slot, generation, ended)
		}
	}
	m, err := vm.New(vmCfg)
	if err != nil {
		t.Fatal(err)
	}
	h.manager = m
	cfg.VMs = m
	ls, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.links, h.cfg = ls, cfg
	t.Cleanup(func() { h.links.Close() })
	return h
}

// timeHook records the call, then runs send.
func (h *harness) timeHook(ctx context.Context, _ vm.Slot, generation uint64, l *guestlink.Link, reason guestv1.TimeReason) error {
	h.reasons <- reason
	h.times <- generation
	if h.send != nil {
		return h.send(ctx, l, reason)
	}
	return nil
}

// mountHook runs mount, or mounts at once.
func (h *harness) mountHook(ctx context.Context, slot vm.Slot, generation uint64, l *guestlink.Link) error {
	if h.mount != nil {
		return h.mount(ctx, slot, generation, l)
	}
	return nil
}

// nextReason returns the reason of the next time hook call.
func (h *harness) nextReason() guestv1.TimeReason {
	h.t.Helper()
	select {
	case r := <-h.reasons:
		return r
	case <-time.After(5 * time.Second):
		h.t.Fatal("the time hook wasn't called")
		return 0
	}
}

func (h *harness) vmConfig(logger *slog.Logger) vm.Config {
	t := h.t
	dataDir := t.TempDir()
	tools := filepath.Join(t.TempDir(), "wb-guest-tools.img")
	if err := os.WriteFile(tools, []byte("tools"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h.db = db
	shim := &datadisktest.Shim{DataDir: dataDir, UUIDs: []string{"0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"}}
	disks, err := datadisk.New(datadisk.Config{DataDir: dataDir, Records: db, Shim: shim, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return vm.Config{
		DataDir: dataDir, ToolsImage: tools, Resources: config.Defaults(),
		Generations: db, Provider: h.provider, Cloner: &vmtest.CopyCloner{}, DataDisks: disks,
		Logger: logger,
	}
}

// connect is the provider's ConnectGuest: a socketpair whose guest end
// wb-guestd's GuestService serves.
func (h *harness) connect(_ context.Context, _ *vmtest.Instance, _ uint64, _ uint32) (net.Conn, error) {
	host, guest := socketpair(h.t)
	if h.users != nil {
		h.users.serve(h.t, guest)
		h.guests <- guest
		h.hosts <- host
		return host, nil
	}
	var opts []guestd.Option
	if h.guestClock != nil {
		opts = append(opts, guestd.WithClock(h.guestClock))
	}
	if h.guestMount != nil {
		opts = append(opts, guestd.WithDataMount(h.guestMount))
	}
	srv := guestd.NewServer(h.guestVersion, slog.New(slog.DiscardHandler), opts...)
	go func() { _ = srv.Serve(newOneConn(guest)) }()
	h.t.Cleanup(srv.Stop)
	// Close the guest end before Stop, which otherwise waits for the
	// handshake of a connection the test never used.
	h.t.Cleanup(func() { _ = guest.Close() })
	h.guests <- guest
	h.hosts <- host
	return host, nil
}

// nextHost returns the host end of the next connection.
func (h *harness) nextHost() net.Conn {
	h.t.Helper()
	select {
	case c := <-h.hosts:
		return c
	case <-time.After(5 * time.Second):
		h.t.Fatal("no connection was opened")
		return nil
	}
}

func (h *harness) start() {
	h.t.Helper()
	if err := h.manager.Start(h.t.Context(), vm.SlotWork); err != nil {
		h.t.Fatal(err)
	}
}

// nextGuest returns the guest end of the next connection.
func (h *harness) nextGuest() net.Conn {
	h.t.Helper()
	select {
	case g := <-h.guests:
		return g
	case <-time.After(5 * time.Second):
		h.t.Fatal("no connection was opened")
		return nil
	}
}

// nextTime waits for the next time hook call.
func (h *harness) nextTime() uint64 {
	h.t.Helper()
	select {
	case g := <-h.times:
		return g
	case <-time.After(5 * time.Second):
		h.t.Fatal("the time hook wasn't called")
		return 0
	}
}

// waitState waits until the work slot's link is in state.
func (h *harness) waitState(state State) Status {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := h.links.Status(vm.SlotWork)
		if s.State == state {
			return s
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("link is %s (%+v), want %s", s.State, s.Refusal, state)
		}
		time.Sleep(time.Millisecond)
	}
}

// socketpair returns both ends of a Unix stream socketpair, the kind of
// descriptor wb-vmd passes for a vsock connection on macOS
// (X18-vsock-handoff, answer 2).
func socketpair(t *testing.T) (host, guest net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	conns := make([]net.Conn, 2)
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "socketpair")
		c, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = c
		t.Cleanup(func() { _ = c.Close() })
	}
	return conns[0], conns[1]
}

// oneConn is a net.Listener that returns one connection, then waits
// until it is closed.
type oneConn struct {
	c    chan net.Conn
	done chan struct{}
	once sync.Once
	addr net.Addr
}

func newOneConn(c net.Conn) *oneConn {
	l := &oneConn{c: make(chan net.Conn, 1), done: make(chan struct{}), addr: c.LocalAddr()}
	l.c <- c
	return l
}

func (l *oneConn) Accept() (net.Conn, error) {
	select {
	case c := <-l.c:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConn) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *oneConn) Addr() net.Addr { return l.addr }
