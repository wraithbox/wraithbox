package guestlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// own is the host's version in these tests. Tests pass it explicitly and
// never rely on the test binary's own stamp
// (S10-02-packaging-and-signing, "One build version").
const own = "1.2.3-dev+abc"

// fakeGuest answers Hello with a fixed response.
type fakeGuest struct {
	guestv1.UnimplementedGuestServiceServer

	resp *guestv1.HelloResponse
}

func (g *fakeGuest) Hello(context.Context, *guestv1.HelloRequest) (*guestv1.HelloResponse, error) {
	return g.resp, nil
}

// serve runs srv as a guest and returns the host's end of a connection to
// it, over an in-memory buffered connection.
func serve(t *testing.T, srv guestv1.GuestServiceServer) net.Conn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := lis.Dial()
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func answer(v string, packages ...string) *fakeGuest {
	return &fakeGuest{resp: &guestv1.HelloResponse{GuestdVersion: v, ServedPackages: packages}}
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

func (c *logCapture) last(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := bytes.Split(bytes.TrimSpace(c.buf.Bytes()), []byte("\n"))
	rec := map[string]any{}
	if err := json.Unmarshal(lines[len(lines)-1], &rec); err != nil {
		t.Fatalf("log %q: %v", c.buf.String(), err)
	}
	return rec
}

const pkg = "wraithbox.guest.v1"

// TestHelloAccepts: a guest of the host's version that serves the host's
// packages passes, and the link keeps the connection.
func TestHelloAccepts(t *testing.T) {
	conn := serve(t, answer(own, pkg))
	var logs logCapture
	link, err := Hello(t.Context(), conn, own, logs.logger())
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	defer link.Close()
	if link.Version() != own {
		t.Fatalf("Version = %q, want %q", link.Version(), own)
	}
	if rec := logs.last(t); rec["decision"] != "accept" || rec["version"] != `"`+own+`"` {
		t.Fatalf("log %v, want an accept with the quoted version", rec)
	}
}

// TestHelloRefuses: each bad answer is refused with its rule, the
// connection is closed, and the refusal is logged with the rule
// (S04-08-records-and-bounds, "With `wb-guestd`",
// SEC11-root-gains-nothing).
func TestHelloRefuses(t *testing.T) {
	cases := []struct {
		name  string
		guest guestv1.GuestServiceServer
		rule  string
	}{
		{"empty version", answer("", pkg), version.RuleUnstamped},
		{"65 bytes", answer("1.2.3-"+strings.Repeat("a", 59), pkg), version.RuleForm},
		{"control byte", answer("1.2.3\x01", pkg), version.RuleForm},
		{"non-ASCII byte", answer("1.2.3-é", pkg), version.RuleForm},
		{"other version", answer("1.2.4", pkg), version.RuleMismatch},
		{"over 4 KiB", answer(own+strings.Repeat("x", HelloMaxRecvMsgSize), pkg), RuleInvalid},
		{"unimplemented", &guestv1.UnimplementedGuestServiceServer{}, RuleInvalid},
		{"no packages", answer(own), RulePackagesMismatch},
		{"other package", answer(own, "wraithbox.guest.v2"), RulePackagesMismatch},
		{"extra package", answer(own, pkg, "wraithbox.vmd.v1"), RulePackagesMismatch},
		{"17 packages", answer(own, strings.Split(strings.Repeat(pkg+",", 17), ",")[:17]...), RulePackagesForm},
		{"package form", answer(own, "wraithbox.Guest.v1"), RulePackagesForm},
		{"package length", answer(own, "wraithbox."+strings.Repeat("g", 52)+".v1"), RulePackagesForm},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := serve(t, tc.guest)
			var logs logCapture
			link, err := Hello(t.Context(), conn, own, logs.logger())
			requireRefused(t, link, err, tc.rule)
			requireClosed(t, conn)
			if rec := logs.last(t); rec["rule"] != tc.rule || rec["decision"] != "refuse" {
				t.Fatalf("log %v, want a refusal with rule %s", rec, tc.rule)
			}
		})
	}
}

// TestHelloTimeout: a guest that never answers is refused with
// RuleTimeout when the deadline passes.
func TestHelloTimeout(t *testing.T) {
	if HelloTimeout != 5*time.Second {
		t.Fatalf("HelloTimeout = %v, want 5s", HelloTimeout)
	}
	// The guest accepts the connection, then never reads or writes.
	lis := bufconn.Listen(1 << 20)
	accepted := make(chan net.Conn, 1)
	go func() {
		silent, _ := lis.Accept()
		accepted <- silent
	}()
	conn, err := lis.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer (<-accepted).Close()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	link, err := Hello(ctx, conn, own, slog.New(slog.DiscardHandler))
	requireRefused(t, link, err, RuleTimeout)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("refused after %v, want about the deadline", d)
	}
	requireClosed(t, conn)
}

// statusGuest answers Hello at once with an error status.
type statusGuest struct {
	guestv1.UnimplementedGuestServiceServer

	code codes.Code
}

func (g *statusGuest) Hello(context.Context, *guestv1.HelloRequest) (*guestv1.HelloResponse, error) {
	return nil, status.Error(g.code, "guest says so")
}

// TestHelloGuestDeadlineStatus: a DEADLINE_EXCEEDED status the guest
// sends is guest input, refused as invalid, not as the host's timeout.
func TestHelloGuestDeadlineStatus(t *testing.T) {
	conn := serve(t, &statusGuest{code: codes.DeadlineExceeded})
	link, err := Hello(t.Context(), conn, own, slog.New(slog.DiscardHandler))
	requireRefused(t, link, err, RuleInvalid)
	requireClosed(t, conn)
}

// TestHelloCanceled: when the host cancels, the connection is closed and
// the error wraps context.Canceled, with no refusal of the guest.
func TestHelloCanceled(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	accepted := make(chan net.Conn, 1)
	go func() {
		silent, _ := lis.Accept()
		accepted <- silent
	}()
	conn, err := lis.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer (<-accepted).Close()
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	var logs logCapture
	link, err := Hello(ctx, conn, own, logs.logger())
	if link != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Hello = %v, %v; want nil, context.Canceled", link, err)
	}
	if _, ok := errors.AsType[*Refusal](err); ok {
		t.Fatalf("Hello returned a refusal %v for the host's own cancel", err)
	}
	if rec := logs.last(t); rec["rule"] != nil || rec["decision"] != "close" {
		t.Fatalf("log %v, want a close without a rule", rec)
	}
	requireClosed(t, conn)
}

// TestHelloUnstampedHost: a host without a version refuses every guest,
// one that reports the same empty version included.
func TestHelloUnstampedHost(t *testing.T) {
	conn := serve(t, answer("", pkg))
	link, err := Hello(t.Context(), conn, "", slog.New(slog.DiscardHandler))
	requireRefused(t, link, err, version.RuleUnstamped)
}

// TestRefusalHidesGuestText: guest text in a refusal is escaped and cut.
func TestRefusalHidesGuestText(t *testing.T) {
	r := checkPackages([]string{"\x1b]0;evil\x07" + strings.Repeat("z", 300)})
	if r == nil || r.Rule != RulePackagesForm {
		t.Fatalf("refusal %v, want %s", r, RulePackagesForm)
	}
	if strings.ContainsAny(r.Error(), "\x1b\x07") || len(r.Error()) > 300 {
		t.Fatalf("refusal message %q holds raw or uncut guest text", r.Error())
	}
}

func requireRefused(t *testing.T, link *Link, err error, rule string) {
	t.Helper()
	if link != nil {
		_ = link.Close()
		t.Fatalf("Hello accepted, want a refusal with %s", rule)
	}
	r, ok := errors.AsType[*Refusal](err)
	if !ok {
		t.Fatalf("Hello error %v (%T), want a *Refusal", err, err)
	}
	if r.Rule != rule {
		t.Fatalf("rule %s (%v), want %s", r.Rule, err, rule)
	}
}

// requireClosed checks that the host's end of the connection is closed.
func requireClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte{0}); err == nil {
		t.Fatal("the connection is still open after a refusal")
	}
}
