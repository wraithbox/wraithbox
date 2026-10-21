package guestd

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestclock"
)

// MaxSlew is how far off the guest clock may be for a periodic update to
// slew it. Further off, SetTime steps it (S06-06-devices-and-warm-start,
// "Step or slew").
const MaxSlew = time.Second

// The rules of SetTime, for the log and the status message.
const (
	// RuleTimeInvalid: a request with a time of 0 or less, or a reason
	// that isn't one of TimeReason's. SetTime refuses it with
	// InvalidArgument and leaves the clock alone.
	RuleTimeInvalid = "guestd-time-invalid"
	// RuleTimeFailed: the OS refused to set the clock. SetTime answers
	// Internal.
	RuleTimeFailed = "guestd-time-failed"
)

// Clock is the guest's wall clock, which SetTime reads and sets. The
// server's own is time.Now with internal/platform/guestclock. Tests pass
// a fake with WithClock.
type Clock interface {
	Now() time.Time
	// Step sets the clock to t at once.
	Step(t time.Time) error
	// Slew moves the clock by d over time, never backward.
	Slew(d time.Duration) error
}

type systemClock struct{}

func (systemClock) Now() time.Time             { return time.Now() }
func (systemClock) Step(t time.Time) error     { return guestclock.Step(t) }
func (systemClock) Slew(d time.Duration) error { return guestclock.Slew(d) }

// SetTime sets the guest clock to the host's time. It steps the clock
// for a boot, a restore or a wake, and when it is more than MaxSlew off,
// and slews it otherwise, so a running build never sees the wall clock
// go backward. It logs each step, and each slew at debug level, because
// one comes every minute.
func (s *service) SetTime(ctx context.Context, req *guestv1.SetTimeRequest) (*guestv1.SetTimeResponse, error) {
	reason := req.GetReason()
	step, known := stepReasons[reason]
	if !known || req.GetUnixNanos() <= 0 {
		s.log.LogAttrs(ctx, slog.LevelWarn, "time refused",
			slog.String("rule", RuleTimeInvalid), slog.String("decision", "refuse"),
			slog.String("reason", reason.String()), slog.Int64("unix_nanos", req.GetUnixNanos()))
		return nil, status.Error(codes.InvalidArgument, RuleTimeInvalid)
	}
	host := time.Unix(0, req.GetUnixNanos())
	off := host.Sub(s.clock.Now())
	step = step || off > MaxSlew || off < -MaxSlew
	how, level := "slew", slog.LevelDebug
	var err error
	if step {
		how, level = "step", slog.LevelInfo
		err = s.clock.Step(host)
	} else {
		err = s.clock.Slew(off)
	}
	attrs := []slog.Attr{
		slog.String("reason", reason.String()), slog.String("how", how), slog.Duration("offset", off),
	}
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "time not set",
			append(attrs, slog.String("rule", RuleTimeFailed), slog.String("error", err.Error()))...)
		return nil, status.Error(codes.Internal, RuleTimeFailed)
	}
	s.log.LogAttrs(ctx, level, "time set", attrs...)
	return &guestv1.SetTimeResponse{}, nil
}

// stepReasons maps each reason SetTime takes to whether it always steps
// the clock.
var stepReasons = map[guestv1.TimeReason]bool{
	guestv1.TimeReason_TIME_REASON_BOOT:     true,
	guestv1.TimeReason_TIME_REASON_RESTORE:  true,
	guestv1.TimeReason_TIME_REASON_WAKE:     true,
	guestv1.TimeReason_TIME_REASON_PERIODIC: false,
}
