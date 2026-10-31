package guestconn

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
)

// The reconnect waits (S06-11-reconnect-and-recovery, "Reconnect"): 0.1 s
// before the first try of a run and after a drop, doubled after each
// failed try up to 5 s. A connection that didn't open and a refused
// hello are each a failed try. The opens of a VM are also capped at
// MaxOpensPerWindow per OpenWindow.
const (
	FirstWait = 100 * time.Millisecond
	MaxWait   = 5 * time.Second
)

// TimeInterval is how often the link calls the time hook while it is up
// (S06-06-devices-and-warm-start, "When the host sends the time").
const TimeInterval = 60 * time.Second

// run is the control link of one VM run.
type run struct {
	ls         *Links
	vc         *vmConns
	slot       vm.Slot
	generation uint64
	// ctx ends when the run ends or Links closes.
	ctx context.Context
	// kick asks the link to send the queued user removals now.
	kick chan struct{}
	// link is the run's link while it is up and has sent the users, else
	// nil. Links.mu guards it.
	link *guestlink.Link
	// sent holds the removals this link has sent, so a kick sends only
	// the ones queued since. Only the run's own goroutine uses it.
	sent map[removalKey]bool
	// killed holds the projects whose first-connection kill the run has
	// sent. Only the run's own goroutine uses it.
	killed map[string]bool
}

// loop connects, keeps the link while it is up, and connects again after
// a drop or a failed try, until the run ends.
func (r *run) loop() {
	wait := FirstWait
	// The first time call of a generation is its boot's, and so is every
	// one after it until one succeeds (S06-06-devices-and-warm-start,
	// "When the host sends the time").
	reason := guestv1.TimeReason_TIME_REASON_BOOT
	for r.sleep(wait) {
		l, err := r.connect()
		if err != nil {
			wait = min(2*wait, MaxWait)
			continue
		}
		// An accepted hello resets the wait.
		wait = FirstWait
		reason = r.keep(l, reason)
	}
}

// sleep waits d, or longer when the VM's open window is full, and
// reports false when the run ended first.
func (r *run) sleep(d time.Duration) bool {
	if r.ctx.Err() != nil {
		return false
	}
	if room := r.vc.untilRoom(); room > d {
		r.vc.rateLog.LogAttrs(r.ctx, slog.LevelWarn, "guest reconnect waits for the window",
			slog.String("rule", RuleReconnectRate), slog.String("decision", "wait"),
			slog.Uint64("generation", r.generation), slog.Duration("wait", room),
			slog.Int("max_opens", MaxOpensPerWindow), slog.Duration("window", OpenWindow))
		d = room
	}
	select {
	case <-r.ls.clock.After(d):
		return r.ctx.Err() == nil
	case <-r.ctx.Done():
		return false
	}
}

// connect opens a connection to wb-guestd and runs the hello on it.
func (r *run) connect() (*guestlink.Link, error) {
	conn, err := r.ls.open(r.ctx, r.slot, r.generation, guestsock.Port, "")
	if err != nil {
		r.ls.setStatus(r.slot, r.generation, func(s *Status) {
			if s.State != StateRefused {
				s.State = StateConnecting
			}
		})
		return nil, err
	}
	l, err := guestlink.Hello(r.ctx, conn, r.ls.cfg.Version, r.vc.log)
	if err != nil {
		if r.ctx.Err() == nil {
			refusal := helloRefusal(r.slot, r.ls.cfg.Version, err)
			r.ls.setStatus(r.slot, r.generation, func(s *Status) { s.State, s.Refusal = StateRefused, refusal })
		}
		return nil, err
	}
	r.ls.setStatus(r.slot, r.generation, func(s *Status) {
		s.State, s.Refusal, s.GuestVersion = StateLinked, nil, l.Version()
		// A new link is a wb-guestd that may have restarted, so a mount
		// an earlier link saw counts only once this link's MountData
		// answers. Until then CheckSessionStart refuses
		// (S06-05-disks, "Mounting the data volume").
		if r.ls.cfg.MountHook != nil {
			s.DataMount = DataMount{State: MountPending}
		}
	})
	return l, nil
}

// keep holds the link until it drops, the guest leaves a time call, the
// data mount or a user call unanswered, or the run ends. It calls the
// mount hook after the hello, then sends the project users and their
// removals, then calls the linked hook, then the time hook, and the time hook every
// TimeInterval after. After the first time call it sends the VM
// generation's first-connection kills, and any it hasn't sent after each
// later one. It returns the reason of the next link's first
// time call.
func (r *run) keep(l *guestlink.Link, reason guestv1.TimeReason) guestv1.TimeReason {
	defer func() { _ = l.Close() }()
	if !r.mount(l) {
		r.reconnecting()
		return reason
	}
	if !r.users(l) {
		r.lost(l)
		return reason
	}
	r.setLink(l)
	defer r.setLink(nil)
	if hook := r.ls.cfg.OnLinked; hook != nil {
		hook(r.slot, r.generation)
	}
	for {
		var kept bool
		if reason, kept = r.sendTime(l, reason); !kept {
			r.reconnecting()
			return reason
		}
		if !r.firstKills(l) {
			r.lost(l)
			return reason
		}
		if !r.idle(l) {
			return reason
		}
	}
}

// idle waits TimeInterval. Each time it is kicked, it sends the user
// removals this link hasn't sent yet: a kept one waits for the next
// connection (S05-04-naming, "The project user"). It reports false when
// the link is gone or the run ended.
func (r *run) idle(l *guestlink.Link) bool {
	tick := r.ls.clock.After(TimeInterval)
	for {
		select {
		case <-tick:
			return true
		case <-r.kick:
			if r.ls.cfg.Users != nil && r.mounted() && !r.sendRemovals(l) {
				r.lost(l)
				return false
			}
		case <-l.Down():
			r.dropped()
			return false
		case <-r.ctx.Done():
			return false
		}
	}
}

// lost shows the link as connecting again after a call that dropped it:
// as a drop when its connection is down.
func (r *run) lost(l *guestlink.Link) {
	if r.ctx.Err() != nil {
		return
	}
	if isDown(l) {
		r.dropped()
		return
	}
	r.reconnecting()
}

// dropped logs a drop of the link's connection, and shows the link as
// connecting again.
func (r *run) dropped() {
	r.vc.log.LogAttrs(r.ctx, slog.LevelWarn, "guest link dropped",
		slog.String("rule", RuleLinkDropped), slog.String("decision", "reconnect"),
		slog.Uint64("generation", r.generation))
	r.reconnecting()
}

// reconnecting shows the link as connecting again after a drop.
func (r *run) reconnecting() {
	r.ls.setStatus(r.slot, r.generation, func(s *Status) { s.State, s.GuestVersion = StateConnecting, "" })
}

// sendTime calls the time hook with reason. It returns the reason of the
// next call, and whether the link is kept:
//
//   - A call that succeeds makes every later one periodic.
//   - A call that fails on a link already down is logged as the drop,
//     RuleLinkDropped, whatever its code.
//   - A call the guest leaves unanswered, past the link's call deadline
//     or on a failed connection, drops the link with RuleTimeUnanswered.
//     So a wb-guestd that answered the hello and then went silent is
//     reconnected, not shown as linked.
//   - Any other failure is logged with RuleTimeFailed, and the link is
//     kept. A later call steps a guest clock more than 1 s off.
//
// The status code comes from the guest's gRPC server or from grpc-go,
// and the host can't tell which. Either way it only decides between a
// reconnect, which the reconnect waits and the open cap bound, and
// keeping the link.
func (r *run) sendTime(l *guestlink.Link, reason guestv1.TimeReason) (guestv1.TimeReason, bool) {
	hook := r.ls.cfg.TimeHook
	if hook == nil {
		return reason, true
	}
	sent := r.ls.clock.Now()
	err := hook(r.ctx, r.slot, r.generation, l, reason)
	attrs := []slog.Attr{
		slog.Uint64("generation", r.generation), slog.String("reason", reason.String()),
		slog.Duration("took", r.ls.clock.Now().Sub(sent)),
	}
	if err == nil {
		// The periodic update comes every minute, so it logs at debug.
		level := slog.LevelInfo
		if reason == guestv1.TimeReason_TIME_REASON_PERIODIC {
			level = slog.LevelDebug
		}
		r.vc.log.LogAttrs(r.ctx, level, "guest time sent", append(attrs, slog.String("decision", "send"))...)
		return guestv1.TimeReason_TIME_REASON_PERIODIC, true
	}
	if r.ctx.Err() != nil {
		// The run ended during the call, and keep returns.
		return reason, true
	}
	select {
	case <-l.Down():
		// The connection dropped under the call: a drop, whatever the
		// call's code.
		r.dropped()
		return reason, false
	default:
	}
	code := status.Code(err)
	attrs = append(attrs, slog.String("code", code.String()))
	if code == codes.DeadlineExceeded || code == codes.Unavailable {
		r.vc.log.LogAttrs(r.ctx, slog.LevelWarn, "guest time unanswered",
			append(attrs, slog.String("rule", RuleTimeUnanswered), slog.String("decision", "reconnect"))...)
		return reason, false
	}
	r.vc.log.LogAttrs(r.ctx, slog.LevelWarn, "guest time not set",
		append(attrs, slog.String("rule", RuleTimeFailed), slog.String("decision", "keep"))...)
	return reason, true
}
