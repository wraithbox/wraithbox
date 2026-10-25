//go:build unix

package guestconn

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

const (
	boot     = guestv1.TimeReason_TIME_REASON_BOOT
	periodic = guestv1.TimeReason_TIME_REASON_PERIODIC
)

// wantReason checks the reason of the next time hook call.
func (h *harness) wantReason(want guestv1.TimeReason) {
	h.t.Helper()
	if got := h.nextReason(); got != want {
		h.t.Fatalf("time reason %v, want %v", got, want)
	}
}

// The first time call of a generation is the boot's, and every later
// one is periodic: after 60 s, and after a reconnect of the same
// generation. The next generation boots again
// (S06-06-devices-and-warm-start, "When the host sends the time").
func TestC11GuestLinkTimeReasons(t *testing.T) {
	h := newHarness(t, own, own, true)
	h.link()
	h.wantReason(boot)
	h.clock.skipTo(t, TimeInterval)
	h.nextTime()
	h.wantReason(periodic)
	_ = h.nextGuest().Close()
	h.clock.skipTo(t, FirstWait)
	h.nextTime()
	h.wantReason(periodic)
	h.waitState(StateLinked)
	if err := h.manager.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	h.waitState(StateDown)
	h.start()
	h.clock.skipTo(t, FirstWait)
	if g := h.nextTime(); g != 2 {
		t.Fatalf("time hook for generation %d, want 2", g)
	}
	h.wantReason(boot)
}

// A time call the guest leaves unanswered, by the deadline or a failed
// connection, drops the link with guest-time-unanswered, and the link
// connects again after 0.1 s. The time was never set, so the next call is
// the boot's again. Any other failure keeps the link with
// guest-time-failed, and the next call is still the boot's.
func TestC11GuestLinkTimeFailures(t *testing.T) {
	tests := []struct {
		name     string
		code     codes.Code
		rule     string
		wait     time.Duration
		connects int
	}{
		{"deadline", codes.DeadlineExceeded, RuleTimeUnanswered, FirstWait, 2},
		{"unavailable", codes.Unavailable, RuleTimeUnanswered, FirstWait, 2},
		{"refused", codes.InvalidArgument, RuleTimeFailed, TimeInterval, 1},
		{"internal", codes.Internal, RuleTimeFailed, TimeInterval, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, own, own, true)
			h.send = failFirst(tt.code)
			// Not h.link: a dropped link may be connecting again before
			// a check could see it linked.
			h.start()
			h.clock.skipTo(t, FirstWait)
			h.nextTime()
			h.wantReason(boot)
			if d := h.clock.nextWait(t); d != tt.wait {
				t.Fatalf("wait after the failed call = %v, want %v", d, tt.wait)
			}
			if recs := h.logs.withRule(t, tt.rule); len(recs) != 1 || recs[0]["code"] != tt.code.String() {
				t.Fatalf("%s records %v, want one with the code %v", tt.rule, recs, tt.code)
			}
			h.clock.advance(tt.wait)
			h.nextTime()
			h.wantReason(boot)
			h.waitState(StateLinked)
			if n := len(h.provider.Last().Connects()); n != tt.connects {
				t.Fatalf("%d connections, want %d", n, tt.connects)
			}
		})
	}
}

// failFirst is a time hook whose first call fails with code, and every
// later one succeeds.
func failFirst(code codes.Code) func(context.Context, *guestlink.Link, guestv1.TimeReason) error {
	var mu sync.Mutex
	fail := true
	return func(context.Context, *guestlink.Link, guestv1.TimeReason) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			fail = false
			return status.Error(code, "guest text")
		}
		return nil
	}
}

// guestClock is a fake guest clock that records each step.
type guestClock struct {
	mu    sync.Mutex
	steps []time.Time
}

func (c *guestClock) Now() time.Time { return time.Unix(0, 0) }

func (c *guestClock) Step(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = append(c.steps, t)
	return nil
}

func (*guestClock) Slew(time.Duration) error { return nil }

func (c *guestClock) stepped() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.steps...)
}

// SendTime, wb-hostd's hook, sends the host's wall clock with the reason,
// and wb-guestd's SetTime steps its clock to it for a boot.
func TestC11GuestLinkSendTime(t *testing.T) {
	h := newHarness(t, own, own, true)
	gc := &guestClock{}
	h.guestClock = gc
	h.send = func(ctx context.Context, l *guestlink.Link, reason guestv1.TimeReason) error {
		return SendTime(ctx, vm.SlotWork, 1, l, reason)
	}
	before := time.Now()
	h.link()
	h.wantReason(boot)
	h.clock.nextWait(t)
	steps := gc.stepped()
	if len(steps) != 1 || steps[0].Before(before) || steps[0].After(time.Now()) {
		t.Fatalf("guest steps %v, want one to the host's time after %v", steps, before)
	}
	if recs := h.logs.withRule(t, RuleTimeFailed); len(recs) != 0 {
		t.Fatalf("time call failed: %v", recs)
	}
}
