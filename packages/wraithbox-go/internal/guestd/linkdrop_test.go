package guestd

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/stats"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsched"
)

// stubbornTable lists one process in the project range that SIGSTOP
// never stops, so a stop loop runs until a bound or its ctx ends. stops
// is closed at the first SIGSTOP sent.
type stubbornTable struct {
	once  sync.Once
	stops chan struct{}
}

func (*stubbornTable) ByUID(uint32) ([]gp.Proc, error) { return nil, nil }

func (*stubbornTable) InRange(uint32, uint32) ([]gp.Proc, error) {
	return []gp.Proc{{PID: 100, Start: 1, UID: 10000, RUID: 10000, Name: "zsh", State: gp.Running}}, nil
}

func (t *stubbornTable) Signal(gp.Proc, gp.Signal) error {
	t.once.Do(func() { close(t.stops) })
	return nil
}

func (*stubbornTable) Bootout(context.Context, uint32) error { return nil }

func (*stubbornTable) Clear(string, uint32) (guestsched.Cleared, error) {
	return guestsched.Cleared{}, nil
}

// gateClock moves only when a loop sleeps. Its first Sleep closes in
// and waits until out is closed, so a test can act while the stop loop
// runs and holds HoldStops off.
type gateClock struct {
	once    sync.Once
	in, out chan struct{}

	mu  sync.Mutex
	now time.Time
}

func (c *gateClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *gateClock) Sleep(_ context.Context, d time.Duration) {
	c.once.Do(func() { close(c.in); <-c.out })
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// eventLog records events in the order they happen.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (e *eventLog) add(ev string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *eventLog) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.events)
}

// loopEndHandler adds "loop-end" to events when a stop loop logs that
// it ended with processes left. StopAll logs that before HoldStops can
// return.
type loopEndHandler struct{ events *eventLog }

func (loopEndHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h loopEndHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "decision" && a.Value.String() == "processes-left" {
			h.events.add("loop-end")
			return false
		}
		return true
	})
	return nil
}

func (h loopEndHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h loopEndHandler) WithGroup(string) slog.Handler { return h }

// recvBackstop bounds recv. It fires only when the test is already
// broken, so a regression fails with a named message instead of
// hanging until the go test timeout. The events themselves come in
// milliseconds.
const recvBackstop = 10 * time.Second

// recv waits for a value on ch, and fails the test naming what it
// waited for if none comes within recvBackstop.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	timer := time.NewTimer(recvBackstop)
	defer timer.Stop()
	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatalf("no %s within %v", what, recvBackstop)
		var zero T
		return zero
	}
}

// TestLinkWatchRunsToItsBounds: a new connection doesn't end the
// link-drop stop loop. It runs to its bounds, and a shell start's
// HoldStops waits for it (S13-01-process-hardening, "When the link
// drops"). The test waits on events, not on time. The loop logs its
// end before it lets HoldStops return, so the event log shows whether
// HoldStops returned early, with no race between the loop's result and
// HoldStops' return.
func TestLinkWatchRunsToItsBounds(t *testing.T) {
	table := &stubbornTable{stops: make(chan struct{})}
	clock := &gateClock{in: make(chan struct{}), out: make(chan struct{}), now: time.Unix(1_800_000_000, 0)}
	events := &eventLog{}
	results := make(chan procs.StopResult, 2)
	w := &linkWatch{
		loops: procs.New(table, table, clock, slog.New(loopEndHandler{events: events})),
		log:   slog.New(slog.DiscardHandler),
		done:  func(r procs.StopResult) { results <- r },
	}
	c1 := w.TagConn(context.Background(), &stats.ConnTagInfo{})
	w.HandleConn(c1, &stats.ConnBegin{})
	w.HandleConn(c1, &stats.ConnEnd{})
	recv(t, table.stops, "the first SIGSTOP")
	// The loop sent SIGSTOP and waits in its first round's settle.
	recv(t, clock.in, "the first settle of the stop loop")
	c2 := w.TagConn(context.Background(), &stats.ConnTagInfo{})
	w.HandleConn(c2, &stats.ConnBegin{})
	calling := make(chan struct{})
	held := make(chan func(), 1)
	go func() {
		close(calling)
		release := w.loops.HoldStops()
		events.add("held")
		held <- release
	}()
	<-calling
	close(clock.out)
	r := recv(t, results, "the stop loop's result")
	if r.CutShort || r.Running == 0 || r.Rounds != procs.MaxRounds {
		t.Errorf("result %+v, want the loop run to its bound of %d rounds", r, procs.MaxRounds)
	}
	recv(t, held, "HoldStops after the stop loop")()
	if got := events.list(); !slices.Equal(got, []string{"loop-end", "held"}) {
		t.Errorf("events %q, want HoldStops to return after the stop loop ended", got)
	}
	if w.dropped(c1) {
		t.Error("dropped with a new connection up")
	}
	if w.dropped(context.Background()) {
		t.Error("dropped for a ctx without a connection")
	}
	w.HandleConn(c2, &stats.ConnEnd{})
	if !w.dropped(c2) {
		t.Error("not dropped after the last connection ended")
	}
	recv(t, results, "the second stop loop's result")
}
