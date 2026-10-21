//go:build unix

package guestd_test

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
)

// fakeClock is a guest clock that stands at now and records what
// SetTime does to it.
type fakeClock struct {
	now time.Time
	err error

	mu    sync.Mutex
	steps []time.Time
	slews []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Step(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steps = append(c.steps, t)
	return c.err
}

func (c *fakeClock) Slew(d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slews = append(c.slews, d)
	return c.err
}

// setTime runs one SetTime from the host's end of a socketpair, with the
// hello first, as wb-hostd calls it.
func setTime(t *testing.T, clock guestd.Clock, req *guestv1.SetTimeRequest) error {
	t.Helper()
	const v = "1.2.3"
	host, guest := socketpair(t)
	srv := guestd.NewServer(v, slog.New(slog.DiscardHandler), guestd.WithClock(clock))
	go func() { _ = srv.Serve(newOneConn(guest)) }()
	defer srv.Stop()
	link, err := guestlink.Hello(t.Context(), host, v, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	defer link.Close()
	_, err = link.Client().SetTime(t.Context(), req)
	return err
}

// TestSetTime: SetTime steps the clock for a boot, a restore and a wake,
// and when a periodic update finds it more than 1 s off, and slews it by
// the offset otherwise (S06-06-devices-and-warm-start, "Step or slew").
func TestSetTime(t *testing.T) {
	guestNow := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name   string
		reason guestv1.TimeReason
		off    time.Duration
		step   bool
	}{
		{"boot", guestv1.TimeReason_TIME_REASON_BOOT, 10 * time.Millisecond, true},
		{"restore", guestv1.TimeReason_TIME_REASON_RESTORE, -10 * time.Millisecond, true},
		{"wake", guestv1.TimeReason_TIME_REASON_WAKE, 0, true},
		{"periodic, behind", guestv1.TimeReason_TIME_REASON_PERIODIC, 500 * time.Millisecond, false},
		{"periodic, ahead", guestv1.TimeReason_TIME_REASON_PERIODIC, -500 * time.Millisecond, false},
		{"periodic, 1 s behind", guestv1.TimeReason_TIME_REASON_PERIODIC, time.Second, false},
		{"periodic, over 1 s behind", guestv1.TimeReason_TIME_REASON_PERIODIC, time.Second + time.Microsecond, true},
		{"periodic, over 1 s ahead", guestv1.TimeReason_TIME_REASON_PERIODIC, -2 * time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{now: guestNow}
			host := guestNow.Add(tc.off)
			err := setTime(t, clock, &guestv1.SetTimeRequest{UnixNanos: host.UnixNano(), Reason: tc.reason})
			if err != nil {
				t.Fatalf("SetTime: %v", err)
			}
			switch {
			case tc.step && (len(clock.steps) != 1 || !clock.steps[0].Equal(host) || len(clock.slews) != 0):
				t.Fatalf("steps %v, slews %v; want one step to %v", clock.steps, clock.slews, host)
			case !tc.step && (len(clock.slews) != 1 || clock.slews[0] != tc.off || len(clock.steps) != 0):
				t.Fatalf("steps %v, slews %v; want one slew by %v", clock.steps, clock.slews, tc.off)
			}
		})
	}
}

// TestSetTimeRefused: a time of 0 or less, or a reason that isn't one of
// TimeReason's, is refused with InvalidArgument and leaves the clock
// alone. A clock the OS doesn't let wb-guestd set answers Internal.
func TestSetTimeRefused(t *testing.T) {
	good := time.Unix(1_800_000_000, 0).UnixNano()
	periodic := guestv1.TimeReason_TIME_REASON_PERIODIC
	cases := []struct {
		name     string
		req      *guestv1.SetTimeRequest
		clockErr error
		code     codes.Code
		calls    int
	}{
		{"no reason", &guestv1.SetTimeRequest{UnixNanos: good}, nil, codes.InvalidArgument, 0},
		{"unknown reason", &guestv1.SetTimeRequest{UnixNanos: good, Reason: 99}, nil, codes.InvalidArgument, 0},
		{"zero time", &guestv1.SetTimeRequest{Reason: periodic}, nil, codes.InvalidArgument, 0},
		{"negative time", &guestv1.SetTimeRequest{UnixNanos: -1, Reason: periodic}, nil, codes.InvalidArgument, 0},
		{"clock refuses a step", &guestv1.SetTimeRequest{UnixNanos: good, Reason: guestv1.TimeReason_TIME_REASON_BOOT}, errors.New("EPERM"), codes.Internal, 1},
		{"clock refuses a slew", &guestv1.SetTimeRequest{UnixNanos: good, Reason: periodic}, errors.New("EPERM"), codes.Internal, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Unix(1_800_000_000, 0), err: tc.clockErr}
			err := setTime(t, clock, tc.req)
			if status.Code(err) != tc.code {
				t.Fatalf("SetTime = %v, want %v", err, tc.code)
			}
			if calls := len(clock.steps) + len(clock.slews); calls != tc.calls {
				t.Fatalf("clock called %d times, want %d", calls, tc.calls)
			}
		})
	}
}
