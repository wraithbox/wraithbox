package guestconn

import (
	"context"
	"log/slog"
	"time"

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
}

// loop connects, keeps the link while it is up, and connects again after
// a drop or a failed try, until the run ends.
func (r *run) loop() {
	wait := FirstWait
	for r.sleep(wait) {
		l, err := r.connect()
		if err != nil {
			wait = min(2*wait, MaxWait)
			continue
		}
		// An accepted hello resets the wait.
		wait = FirstWait
		r.keep(l)
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
	r.ls.setStatus(r.slot, r.generation, func(s *Status) { s.State, s.Refusal, s.GuestVersion = StateLinked, nil, l.Version() })
	return l, nil
}

// keep holds the link until it drops or the run ends, and calls the time
// hook after the hello and every TimeInterval.
func (r *run) keep(l *guestlink.Link) {
	defer func() { _ = l.Close() }()
	r.sendTime(l)
	for {
		select {
		case <-r.ls.clock.After(TimeInterval):
			r.sendTime(l)
		case <-l.Down():
			r.vc.log.LogAttrs(r.ctx, slog.LevelWarn, "guest link dropped",
				slog.String("rule", RuleLinkDropped), slog.String("decision", "reconnect"),
				slog.Uint64("generation", r.generation))
			r.ls.setStatus(r.slot, r.generation, func(s *Status) { s.State, s.GuestVersion = StateConnecting, "" })
			return
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *run) sendTime(l *guestlink.Link) {
	if hook := r.ls.cfg.TimeHook; hook != nil {
		hook(r.ctx, r.slot, r.generation, l)
	}
}
