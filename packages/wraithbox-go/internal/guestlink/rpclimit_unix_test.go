//go:build unix

package guestlink

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// fakeNow is a clock that moves only when a test moves it.
type fakeNow struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeNow) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeNow) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// gateHandler is a test service on wb-hostd's side whose Hello waits
// until the gate opens, and counts the calls running.
type gateHandler struct {
	guestv1.UnimplementedGuestServiceServer

	gate    chan struct{}
	running atomic.Int32
	entered chan struct{}
}

func newGate() *gateHandler {
	return &gateHandler{gate: make(chan struct{}), entered: make(chan struct{}, 1024)}
}

func (h *gateHandler) Hello(ctx context.Context, _ *guestv1.HelloRequest) (*guestv1.HelloResponse, error) {
	h.running.Add(1)
	defer h.running.Add(-1)
	h.entered <- struct{}{}
	select {
	case <-h.gate:
	case <-ctx.Done():
	}
	return &guestv1.HelloResponse{}, nil
}

// guestClient serves h with NewServer(rpc) on the host end of a new
// socketpair, and returns a plain gRPC client on the guest end, as a
// guest would dial it.
func guestClient(t *testing.T, rpc *RPCLimiter, h guestv1.GuestServiceServer) guestv1.GuestServiceClient {
	t.Helper()
	host, guest := socketpair(t)
	srv := NewServer(rpc)
	guestv1.RegisterGuestServiceServer(srv, h)
	serveOn(t, srv, host)
	cc, err := grpc.NewClient("passthrough:///host",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return guest, nil }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return guestv1.NewGuestServiceClient(cc)
}

// requireRule checks that err is RESOURCE_EXHAUSTED with rule as its
// ErrorInfo reason.
func requireRule(t *testing.T, err error, rule string) {
	t.Helper()
	st := status.Convert(err)
	if st.Code() != codes.ResourceExhausted {
		t.Fatalf("error %v, want RESOURCE_EXHAUSTED with %s", err, rule)
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetReason() == rule {
			return
		}
	}
	t.Fatalf("error %v has no ErrorInfo with reason %s", err, rule)
}

// TestRPCRate: past the burst, a request is refused with guest-rpc-rate
// and logged with the rule. The bucket refills at the rate.
func TestRPCRate(t *testing.T) {
	clk := &fakeNow{now: time.Unix(1000, 0)}
	var logs logCapture
	rpc := newRPCLimiter("work", DefaultRPCLimits(), logs.logger(), clk.Now)
	h := newGate()
	close(h.gate)
	c := guestClient(t, rpc, h)
	for i := range DefaultRPCBurst {
		if _, err := c.Hello(t.Context(), &guestv1.HelloRequest{}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	_, err := c.Hello(t.Context(), &guestv1.HelloRequest{})
	requireRule(t, err, RuleRPCRate)
	if rec := logs.last(t); rec["rule"] != RuleRPCRate || rec["vm"] != "work" || rec["decision"] != "refuse" {
		t.Fatalf("log %v, want a refusal with %s", rec, RuleRPCRate)
	}
	clk.add(time.Second)
	for i := range DefaultRPCRate {
		if _, err := c.Hello(t.Context(), &guestv1.HelloRequest{}); err != nil {
			t.Fatalf("request %d after a second: %v", i, err)
		}
	}
	_, err = c.Hello(t.Context(), &guestv1.HelloRequest{})
	requireRule(t, err, RuleRPCRate)
}

// TestRPCInFlight: the VM's bound of requests in flight holds over all
// of its connections, and a request past it is refused at once with
// guest-rpc-inflight.
func TestRPCInFlight(t *testing.T) {
	var logs logCapture
	rpc := NewRPCLimiter("work", DefaultRPCLimits(), logs.logger())
	h := newGate()
	// Three connections, since one takes at most 16 streams at once.
	clients := []guestv1.GuestServiceClient{guestClient(t, rpc, h), guestClient(t, rpc, h), guestClient(t, rpc, h)}
	var wg sync.WaitGroup
	for i := range DefaultRPCInFlight {
		wg.Go(func() { _, _ = clients[i%3].Hello(t.Context(), &guestv1.HelloRequest{}) })
	}
	for range DefaultRPCInFlight {
		<-h.entered
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := clients[0].Hello(ctx, &guestv1.HelloRequest{})
	requireRule(t, err, RuleRPCInFlight)
	if rec := logs.last(t); rec["rule"] != RuleRPCInFlight {
		t.Fatalf("log %v, want a refusal with %s", rec, RuleRPCInFlight)
	}
	close(h.gate)
	wg.Wait()
	if _, err := clients[1].Hello(t.Context(), &guestv1.HelloRequest{}); err != nil {
		t.Fatalf("a request after the others ended: %v", err)
	}
}

// TestServerStreamsPerConnection: one guest connection runs at most 16
// requests at once, whatever the VM's bounds allow.
func TestServerStreamsPerConnection(t *testing.T) {
	rpc := NewRPCLimiter("work", RPCLimits{Rate: 1000, Burst: 1000, InFlight: 1000}, discard)
	h := newGate()
	c := guestClient(t, rpc, h)
	var wg sync.WaitGroup
	for range guestLimits.maxStreams + 1 {
		wg.Go(func() { _, _ = c.Hello(t.Context(), &guestv1.HelloRequest{}) })
	}
	for range guestLimits.maxStreams {
		<-h.entered
	}
	select {
	case <-h.entered:
		t.Fatalf("%d requests run at once on one connection", h.running.Load())
	case <-time.After(200 * time.Millisecond):
	}
	close(h.gate)
	wg.Wait()
}

// TestServerReceiveLimit: a request over 64 KiB is refused before the
// handler sees it.
func TestServerReceiveLimit(t *testing.T) {
	rpc := NewRPCLimiter("work", DefaultRPCLimits(), discard)
	h := newGate()
	close(h.gate)
	c := guestClient(t, rpc, h)
	big := &guestv1.HelloRequest{}
	raw := protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), make([]byte, guestLimits.maxMsgSize))
	if err := proto.Unmarshal(raw, big); err != nil {
		t.Fatal(err)
	}
	_, err := c.Hello(t.Context(), big)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("a request over 64 KiB: %v, want RESOURCE_EXHAUSTED", err)
	}
	if len(h.entered) != 0 {
		t.Fatal("the handler ran for a request over the limit")
	}
}

// TestAdmitReleasesOnce: a release called twice frees one slot.
func TestAdmitReleasesOnce(t *testing.T) {
	clk := &fakeNow{now: time.Unix(1000, 0)}
	rpc := newRPCLimiter("work", RPCLimits{Rate: 100, Burst: 100, InFlight: 1}, discard, clk.Now)
	rule, release := rpc.admit()
	if rule != "" {
		t.Fatal(rule)
	}
	if rule, _ := rpc.admit(); rule != RuleRPCInFlight {
		t.Fatalf("second admit: %q, want %s", rule, RuleRPCInFlight)
	}
	release()
	release()
	rule, release = rpc.admit()
	if rule != "" {
		t.Fatalf("admit after the release: %q", rule)
	}
	if rule, _ := rpc.admit(); rule != RuleRPCInFlight {
		t.Fatalf("a double release freed two slots: %q", rule)
	}
	release()
}
