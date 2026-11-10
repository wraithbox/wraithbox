package stream

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
)

// The hosts and ports of the test policies.
const (
	inspectHost = "api.example.com"
	passHost    = "pass.example.com"
	rawHost     = "db.example.com"
	rawPort     = 5432
	waitLong    = 10 * time.Second
)

func mustName(t testing.TB, s string) dnsname.Name {
	t.Helper()
	n, ref := dnsname.FromHost(s)
	if ref != nil {
		t.Fatalf("FromHost(%q): %v", s, ref)
	}
	return n
}

// testPolicy inspects inspectHost on 80 and 443, passes passHost on 443,
// and has a raw TCP entry for rawHost on rawPort.
func testPolicy(t testing.TB) *policy.Policy {
	t.Helper()
	return &policy.Policy{
		Generation: 1,
		Endpoints: []policy.Endpoint{
			{Host: match.ExactHost(mustName(t, inspectHost)), Ports: []uint16{80, 443}},
			{Host: match.ExactHost(mustName(t, passHost)), Ports: []uint16{443}},
		},
		PassHosts: []policy.PassHost{{
			Host: mustName(t, passHost), Source: policy.Source{Kind: policy.SourceProject, Project: "demo"}, Reason: "pinned client",
		}},
		RawTCP: []policy.RawTCP{{
			Host: mustName(t, rawHost), Port: rawPort, Source: policy.Source{Kind: policy.SourceProject, Project: "demo"}, Reason: "database",
		}},
	}
}

// fakeCtl is the hand-off's side of a stream: Reset and Close close the
// stream's connection, a reset with no linger, so the guest sees a TCP
// reset, and record how the stream ended. Only the first end counts, as
// on a handoff.Stream. setPolicy runs the checks as a recompute does,
// and resets the stream with policy-recomputed when one fails, and
// AddCheck does when its check fails at once. SendWhileOpen checks the
// end and sends under endMu, which end takes too, as on a
// handoff.Stream. beforeSend, when set, runs as SendWhileOpen is
// called, before its check. The hand-off's own log records aren't
// written.
type fakeCtl struct {
	conn  net.Conn
	ended chan string
	done  chan struct{}
	once  sync.Once
	endMu sync.Mutex

	beforeSend func()

	mu     sync.Mutex
	p      *policy.Policy
	checks []func(*policy.Policy) egressmsg.Check
}

func (c *fakeCtl) end(how string) bool {
	c.endMu.Lock()
	defer c.endMu.Unlock()
	did := false
	c.once.Do(func() {
		did = true
		close(c.done)
		if how != "close" {
			if tc, ok := c.conn.(*net.TCPConn); ok {
				_ = tc.SetLinger(0)
			}
		}
		_ = c.conn.Close()
		c.ended <- how
	})
	return did
}

func (c *fakeCtl) Reset() bool           { return c.end("reset") }
func (c *fakeCtl) Close() bool           { return c.end("close") }
func (c *fakeCtl) Done() <-chan struct{} { return c.done }

func (c *fakeCtl) SendWhileOpen(send func() error) error {
	if c.beforeSend != nil {
		c.beforeSend()
	}
	c.endMu.Lock()
	defer c.endMu.Unlock()
	select {
	case <-c.done:
		return handoff.ErrEnded
	default:
	}
	return send()
}

// handoffReset ends the stream as a recompute or the socket's end does.
func (c *fakeCtl) handoffReset() { c.end(handoffEnd) }

// handoffEnd is how fakeCtl records a stream the hand-off ended.
const handoffEnd = "hand-off"

func (c *fakeCtl) AddCheck(check func(*policy.Policy) egressmsg.Check) egressmsg.Check {
	c.mu.Lock()
	c.checks = append(c.checks, check)
	p := c.p
	c.mu.Unlock()
	failed := check(p)
	if failed != "" {
		c.end(handoff.RulePolicyRecomputed)
	}
	return failed
}

func (c *fakeCtl) Policy() *policy.Policy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.p
}

func (c *fakeCtl) setPolicy(p *policy.Policy) {
	c.mu.Lock()
	c.p = p
	checks := append([]func(*policy.Policy) egressmsg.Check(nil), c.checks...)
	c.mu.Unlock()
	for _, check := range checks {
		if check(p) != "" {
			c.end(handoff.RulePolicyRecomputed)
			return
		}
	}
}

// syncBuffer is a log destination the handler and the test share.
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

type fakeAudit struct {
	mu     sync.Mutex
	events []handoff.Event
}

func (a *fakeAudit) Refused(e handoff.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

// fakeInspector takes inspected streams and closes them.
type fakeInspector struct {
	got chan *Inspected
}

func (i *fakeInspector) Inspect(s *Inspected) {
	i.got <- s
}

// testDialer dials addr whatever the host, and counts its dials. With
// inner set, it asks inner instead, for 127.0.0.1 and the stream's port.
// onDial, when set, runs first, and afterDial once a dial opened. With
// block set, a dial waits for its context to end and fails.
type testDialer struct {
	addr      string
	inner     Dialer
	onDial    func()
	afterDial func()
	block     bool
	dials     atomic.Int32
}

func (d *testDialer) Dial(ctx context.Context, _ string, port uint16) (net.Conn, error) {
	d.dials.Add(1)
	if d.onDial != nil {
		d.onDial()
	}
	if d.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if d.inner != nil {
		return d.inner.Dial(ctx, "127.0.0.1", port)
	}
	var dialer net.Dialer
	c, err := dialer.DialContext(ctx, "tcp", d.addr)
	if err == nil && d.afterDial != nil {
		d.afterDial()
	}
	return c, err
}

// harness is one handler and one stream to it.
type harness struct {
	t         *testing.T
	h         *Handler
	logs      *syncBuffer
	audit     *fakeAudit
	dialer    *testDialer
	inspector *fakeInspector
	ctl       *fakeCtl
	// guest is the guest's end of the stream.
	guest net.Conn
	done  chan struct{}
}

// newHarness returns a handler that dials upstream at addr.
func newHarness(t *testing.T, addr string) *harness {
	t.Helper()
	logs := &syncBuffer{}
	x := &harness{
		t: t, logs: logs, audit: &fakeAudit{},
		dialer:    &testDialer{addr: addr},
		inspector: &fakeInspector{got: make(chan *Inspected, 1)},
		done:      make(chan struct{}),
	}
	h, err := New(Config{
		Dialer: x.dialer, Inspector: x.inspector, Audit: x.audit,
		Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	x.h = h
	return x
}

// start serves one stream to host and port under p, and returns once
// the handler runs.
func (x *harness) start(p *policy.Policy, host string, port uint16) {
	x.t.Helper()
	guest, proxy := tcpPair(x.t)
	x.guest = guest
	x.ctl = &fakeCtl{conn: proxy, ended: make(chan string, 1), done: make(chan struct{}), p: p}
	st := &stream{
		vm: "vm-1", gen: 3, id: 7, name: mustName(x.t, host), port: port,
		conn: proxy, ctl: x.ctl, began: time.Now(),
	}
	go func() {
		defer close(x.done)
		x.h.serve(st)
	}()
}

// ended waits for the stream to end and returns how.
func (x *harness) ended() string {
	x.t.Helper()
	select {
	case how := <-x.ctl.ended:
		return how
	case <-time.After(waitLong):
		x.t.Fatalf("the stream didn't end; log:\n%s", x.logs.String())
	}
	return ""
}

// wantRefused checks the stream was reset or closed and the refusal was
// logged once, with the rule, check and what the bytes looked like.
func (x *harness) wantRefused(how, rule string, check egressmsg.Check, looks string) {
	x.t.Helper()
	if got := x.ended(); got != how {
		x.t.Errorf("stream ended with %q, want %q", got, how)
	}
	<-x.done
	// A relay's refusal after its dial follows its one allowed record,
	// and its end is logged too. Any other refusal is the only record.
	allowed := 0
	if strings.Contains(x.logs.String(), "egress relay ended") {
		allowed = 1
	}
	x.wantRecords(allowed, 1)
	logs := x.logs.String()
	decision := decisionReset
	if how == "close" {
		decision = decisionClose
	}
	want := []string{"decision=" + decision, "rule=" + rule}
	if check != "" {
		want = append(want, "check="+string(check))
	}
	if looks != "" {
		want = append(want, "looks_like="+looks)
	}
	for _, w := range want {
		if !strings.Contains(logs, w) {
			x.t.Errorf("log lacks %q:\n%s", w, logs)
		}
	}
	x.audit.mu.Lock()
	defer x.audit.mu.Unlock()
	if len(x.audit.events) != 1 || x.audit.events[0].Rule != rule {
		x.t.Errorf("audit events = %+v, want one with rule %q", x.audit.events, rule)
	}
}

// wantRecords checks the log holds exactly allowed records of an
// allowed stream and refused records of a refusal, and the audit hook
// as many refusals.
func (x *harness) wantRecords(allowed, refused int) {
	x.t.Helper()
	logs := x.logs.String()
	gotAllowed := strings.Count(logs, "egress stream allowed")
	gotRefused := strings.Count(logs, "egress stream refused")
	x.audit.mu.Lock()
	events := len(x.audit.events)
	x.audit.mu.Unlock()
	if gotAllowed != allowed || gotRefused != refused || events != refused {
		x.t.Errorf("%d allowed and %d refused records, %d audit events; want %d, %d and %d; log:\n%s",
			gotAllowed, gotRefused, events, allowed, refused, refused, logs)
	}
}

// wantAllowed checks the log holds exactly one allowed record, with the
// rule and each of want, and no refusal.
func (x *harness) wantAllowed(rule string, want ...string) {
	x.t.Helper()
	x.wantRecords(1, 0)
	logs := x.logs.String()
	for _, w := range append([]string{"rule=" + rule, "decision=allow"}, want...) {
		if !strings.Contains(logs, w) {
			x.t.Errorf("log lacks %q:\n%s", w, logs)
		}
	}
}

// wantInspected waits for the stream at the Inspector.
func (x *harness) wantInspected() *Inspected {
	x.t.Helper()
	select {
	case s := <-x.inspector.got:
		return s
	case how := <-x.ctl.ended:
		x.t.Fatalf("stream ended with %q; log:\n%s", how, x.logs.String())
	case <-time.After(waitLong):
		x.t.Fatal("no stream at the Inspector")
	}
	return nil
}

func (x *harness) wantNoDial() {
	x.t.Helper()
	if n := x.dialer.dials.Load(); n != 0 {
		x.t.Errorf("%d upstream dials, want none", n)
	}
}

func (x *harness) send(b []byte) {
	x.t.Helper()
	if _, err := x.guest.Write(b); err != nil {
		x.t.Fatalf("guest write: %v", err)
	}
}

// tcpPair returns the two ends of a loopback TCP connection.
func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln := listen(t)
	defer func() { _ = ln.Close() }()
	type result struct {
		c   net.Conn
		err error
	}
	accepted := make(chan result, 1)
	go func() {
		c, err := ln.Accept()
		accepted <- result{c, err}
	}()
	var dialer net.Dialer
	a, err := dialer.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := <-accepted
	if r.err != nil {
		t.Fatal(r.err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = r.c.Close() })
	return a, r.c
}

// server is an upstream test server that takes one connection and runs
// script on it. got holds every byte it read with readUntil and
// readRest.
type server struct {
	ln   net.Listener
	got  syncBuffer
	done chan struct{}
}

func newServer(t *testing.T, script func(s *server, c net.Conn)) *server {
	t.Helper()
	ln := listen(t)
	s := &server{ln: ln, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(waitLong))
		script(s, c)
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *server) addr() string { return s.ln.Addr().String() }

// read reads n bytes and records them.
func (s *server) read(c net.Conn, n int) {
	b := make([]byte, n)
	k, _ := io.ReadFull(c, b)
	_, _ = s.got.Write(b[:k])
}

// readRest reads until the connection ends and records the bytes.
func (s *server) readRest(c net.Conn) {
	b, _ := io.ReadAll(c)
	_, _ = s.got.Write(b)
}

// received waits for the script to end and returns what it read.
func (s *server) received(t *testing.T) []byte {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(waitLong):
		t.Fatal("the upstream server didn't end")
	}
	return []byte(s.got.String())
}

// listen listens on a loopback port.
func listen(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// closeWrite half-closes the guest's end of the stream.
func (x *harness) closeWrite() {
	x.t.Helper()
	tc, ok := x.guest.(*net.TCPConn)
	if !ok {
		x.t.Fatalf("guest is a %T", x.guest)
	}
	if err := tc.CloseWrite(); err != nil {
		x.t.Fatal(err)
	}
}
