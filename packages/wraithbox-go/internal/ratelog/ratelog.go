// Package ratelog bounds how many log records a peer can cause. A
// handler from it writes at most one record per rule in each window, and
// the next record of that rule it writes carries the count it held back
// (S04-08-records-and-bounds, "Host work the guest can cause").
//
// A record names its rule in a "rule" attribute, the convention of every
// refusal log in this module. Records without one pass through: their
// rate is bounded by the work that makes them.
package ratelog

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Window is how often a record of one rule is written at most, the
// window of wb-guestd's own listener (S04-02-vm-provider, "Guest ports
// below 1024").
const Window = 10 * time.Second

// maxRules bounds the rules a handler keeps a window for. Rules are
// names from this module's code, so there are far fewer. Past the bound
// every further rule shares one window.
const maxRules = 64

// otherRule is the shared window of the rules past maxRules.
const otherRule = "\x00other"

// HeldBackKey is the attribute that carries the count of records held
// back since the last one of the rule.
const HeldBackKey = "held_back"

// limits is the state that a handler and every handler derived from it
// share.
type limits struct {
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	rules map[string]*rule
}

type rule struct {
	next time.Time // when the next record may be written
	held int       // records held back since the last one written
}

// allow reports whether a record of name may be written now, and how
// many were held back since the last one.
func (l *limits) allow(name string) (ok bool, held int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, found := l.rules[name]
	if !found && len(l.rules) >= maxRules {
		name = otherRule
		r, found = l.rules[name]
	}
	if !found {
		r = &rule{}
		l.rules[name] = r
	}
	now := l.now()
	if now.Before(r.next) {
		r.held++
		return false, 0
	}
	held, r.held = r.held, 0
	r.next = now.Add(l.window)
	return true, held
}

// handler is a slog.Handler that drops records of a rule within its
// window.
type handler struct {
	next   slog.Handler
	limits *limits
	// rule is set when WithAttrs gave the rule to every record.
	rule string
}

// New returns a logger that writes through log's handler, at most one
// record per rule per Window.
func New(log *slog.Logger) *slog.Logger { return NewAt(log, Window, time.Now) }

// NewAt is New with its window and clock, for tests.
func NewAt(log *slog.Logger, window time.Duration, now func() time.Time) *slog.Logger {
	return slog.New(&handler{
		next:   log.Handler(),
		limits: &limits{window: window, now: now, rules: map[string]*rule{}},
	})
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	name := h.rule
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "rule" {
			name = a.Value.String()
			return false
		}
		return true
	})
	if name == "" {
		return h.next.Handle(ctx, r)
	}
	ok, held := h.limits.allow(name)
	if !ok {
		return nil
	}
	if held > 0 {
		r = r.Clone()
		r.AddAttrs(slog.Int(HeldBackKey, held))
	}
	return h.next.Handle(ctx, r)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := &handler{next: h.next.WithAttrs(attrs), limits: h.limits, rule: h.rule}
	for _, a := range attrs {
		if a.Key == "rule" {
			out.rule = a.Value.String()
		}
	}
	return out
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(name), limits: h.limits, rule: h.rule}
}
