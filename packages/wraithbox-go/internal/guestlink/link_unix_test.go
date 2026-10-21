//go:build unix

package guestlink

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// scriptGuest answers each Hello with the next answer of its script, the
// last one again once the script runs out, and keeps the metadata of
// every call.
type scriptGuest struct {
	guestv1.UnimplementedGuestServiceServer

	mu      sync.Mutex
	answers []*guestv1.HelloResponse
	calls   []metadata.MD
}

func (g *scriptGuest) Hello(ctx context.Context, _ *guestv1.HelloRequest) (*guestv1.HelloResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, md)
	i := min(len(g.calls), len(g.answers)) - 1
	return g.answers[i], nil
}

func (g *scriptGuest) metadata() []metadata.MD {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]metadata.MD(nil), g.calls...)
}

func good() *guestv1.HelloResponse {
	return &guestv1.HelloResponse{GuestdVersion: own, ServedPackages: ownPackages()}
}

// link serves g on the guest end of a socketpair, and runs Hello on the
// host end.
func link(t *testing.T, g *scriptGuest) (l *Link, guestEnd, hostEnd net.Conn) {
	t.Helper()
	host, guest := socketpair(t)
	srv := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(srv, g)
	serveOn(t, srv, guest)
	l, err := Hello(t.Context(), host, own, discard)
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, guest, host
}

// TestLinkNamesItself: the guest sees :authority wb-guestd and a fixed
// user agent, never a host path (S04-08-records-and-bounds, "RPCs from
// `wb-guestd`").
func TestLinkNamesItself(t *testing.T) {
	g := &scriptGuest{answers: []*guestv1.HelloResponse{good()}}
	link(t, g)
	md := g.metadata()
	if len(md) != 1 {
		t.Fatalf("%d calls, want 1", len(md))
	}
	if got := md[0].Get(":authority"); len(got) != 1 || got[0] != authority {
		t.Errorf(":authority = %q, want %q", got, authority)
	}
	ua := md[0].Get("user-agent")
	if len(ua) != 1 || !strings.HasPrefix(ua[0], userAgent+" grpc-go/") {
		t.Errorf("user-agent = %q, want %q and grpc-go's version only", ua, userAgent)
	}
	for _, vs := range md[0] {
		for _, v := range vs {
			if strings.Contains(v, "/") && !strings.HasPrefix(v, userAgent+" grpc-go/") && !strings.HasPrefix(v, "application/grpc") {
				t.Errorf("metadata value %q looks like a path", v)
			}
		}
	}
}

// TestLinkReceiveLimit: after the Hello, calls on the link take the
// connection's 64 KiB cap, not grpc-go's 4 MiB default, and an answer
// over it is refused before it is decoded.
func TestLinkReceiveLimit(t *testing.T) {
	big := func(n int) *guestv1.HelloResponse {
		return &guestv1.HelloResponse{GuestdVersion: strings.Repeat("v", n)}
	}
	g := &scriptGuest{answers: []*guestv1.HelloResponse{good(), big(32 << 10), big(guestLimits.maxMsgSize)}}
	l, _, _ := link(t, g)
	if _, err := l.Client().Hello(t.Context(), &guestv1.HelloRequest{}); err != nil {
		t.Fatalf("a 32 KiB answer: %v", err)
	}
	_, err := l.Client().Hello(t.Context(), &guestv1.HelloRequest{})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("an answer over 64 KiB: %v, want RESOURCE_EXHAUSTED", err)
	}
}

// TestLinkDrop: when the guest end closes, the link goes down, its
// connection is closed, and gRPC never asked the dialer again.
func TestLinkDrop(t *testing.T) {
	g := &scriptGuest{answers: []*guestv1.HelloResponse{good()}}
	l, guest, host := link(t, g)
	_ = guest.Close()
	select {
	case <-l.Down():
	case <-time.After(5 * time.Second):
		t.Fatal("the link isn't down after the guest end closed")
	}
	if _, err := host.Write([]byte{0}); err == nil {
		t.Error("the host end is still open after the drop")
	}
	if _, err := l.Client().Hello(t.Context(), &guestv1.HelloRequest{}); err == nil {
		t.Error("a call on a dropped link succeeded")
	}
	if n := l.Dials(); n != 1 {
		t.Fatalf("the dialer was called %d times, want once", n)
	}
	if err := l.Close(); err != nil {
		t.Logf("Close after the drop: %v", err)
	}
}

// TestLinkClose: Close ends the watch and closes the connection, and a
// second Close is harmless.
func TestLinkClose(t *testing.T) {
	g := &scriptGuest{answers: []*guestv1.HelloResponse{good()}}
	l, _, host := link(t, g)
	_ = l.Close()
	_ = l.Close()
	select {
	case <-l.Down():
	default:
		t.Fatal("Down isn't closed after Close")
	}
	if _, err := host.Write([]byte{0}); err == nil {
		t.Error("the host end is still open after Close")
	}
}

// TestDialerRefusesSecondCall: when grpc-go asks the link's dialer for a
// second connection, the dialer refuses and logs RuleRedial.
func TestDialerRefusesSecondCall(t *testing.T) {
	host, guest := socketpair(t)
	srv := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(srv, &scriptGuest{answers: []*guestv1.HelloResponse{good()}})
	serveOn(t, srv, guest)
	var logs logCapture
	cc, dials, err := newClient(host, logs.logger())
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	if _, err := guestv1.NewGuestServiceClient(cc).Hello(t.Context(), &guestv1.HelloRequest{}); err != nil {
		t.Fatal(err)
	}
	srv.Stop()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for s := cc.GetState(); s == connectivity.Ready; s = cc.GetState() {
		if !cc.WaitForStateChange(ctx, s) {
			t.Fatal("the connection stayed READY")
		}
	}
	callCtx, callCancel := context.WithTimeout(t.Context(), time.Second)
	defer callCancel()
	if _, err := guestv1.NewGuestServiceClient(cc).Hello(callCtx, &guestv1.HelloRequest{}); err == nil {
		t.Fatal("a call after the drop succeeded")
	}
	if dials.Load() < 2 {
		t.Fatalf("the dialer was called %d times, want a refused second call", dials.Load())
	}
	if rec := logs.last(t); rec["rule"] != RuleRedial || rec["decision"] != "refuse" {
		t.Fatalf("log %v, want a refusal with %s", rec, RuleRedial)
	}
}
