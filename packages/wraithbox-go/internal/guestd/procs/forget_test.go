package procs_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
)

// TestForget: a key Forget drops is no longer the stop loop's, so a
// later stop of that process counts as someone else's.
func TestForget(t *testing.T) {
	table := stopTable()
	l, _, _ := newLoops(t, table)
	l.StopAll(t.Context(), "link-drop")
	l.Forget([]gp.Key{table.procs[0].Key()})
	if got := l.StoppedByUs(); len(got) != 1 || got[0].PID != 101 {
		t.Fatalf("StoppedByUs = %v", got)
	}
	res := l.StopAll(t.Context(), "start")
	if res.Stopped != 1 || res.AlreadyStopped != 2 {
		t.Errorf("StopAll after Forget = %+v", res)
	}
}

// TestKillForgets: the kill loop drops the keys of the processes it
// kills.
func TestKillForgets(t *testing.T) {
	table := stopTable()
	l, _, _ := newLoops(t, table)
	l.StopAll(t.Context(), "link-drop")
	l.Kill(t.Context(), 10000)
	if got := l.StoppedByUs(); len(got) != 1 || got[0].PID != 101 {
		t.Errorf("StoppedByUs after the kill = %v", got)
	}
}

// TestStopAllCutShort: a ctx that ends the loop with a process still
// running sets CutShort and logs the decision with the rule.
func TestStopAllCutShort(t *testing.T) {
	table := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}, unstoppable: map[int32]bool{100: true}}
	l, _, log := newLoops(t, table)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res := l.StopAll(ctx, "link-drop")
	if !res.CutShort || res.Rounds != 0 || res.Running != 1 {
		t.Errorf("StopAll = %+v", res)
	}
	if !strings.Contains(log.String(), `"decision":"cut-short"`) || !strings.Contains(log.String(), procs.RuleStopIncomplete) {
		t.Errorf("log %s", log)
	}
}

// TestHoldStopsWaitsForALoop: HoldStops returns only once a running
// stop loop ends.
func TestHoldStopsWaitsForALoop(t *testing.T) {
	l, gate := gatedLoops()
	done := make(chan struct{})
	go func() { l.StopAll(context.Background(), "link-drop"); close(done) }()
	<-gate.in
	held := make(chan func())
	go func() { held <- l.HoldStops() }()
	wantBlocked(t, held, "HoldStops returned while a stop loop ran")
	close(gate.out)
	<-done
	(<-held)()
}

// TestHoldStopsHoldsALoop: a stop loop waits for a held start to
// release, and HoldStops doesn't wait for a kill loop.
func TestHoldStopsHoldsALoop(t *testing.T) {
	l, gate := gatedLoops()
	killed := make(chan struct{})
	go func() { l.Kill(context.Background(), 10000); close(killed) }()
	<-gate.in
	held := make(chan func(), 1)
	go func() { held <- l.HoldStops() }()
	var release func()
	select {
	case release = <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("HoldStops waited for a kill loop")
	}
	stopped := make(chan procs.StopResult)
	go func() { stopped <- l.StopAll(context.Background(), "link-drop") }()
	wantBlocked(t, stopped, "a stop loop ran during a held start")
	release()
	close(gate.out)
	<-killed
	<-stopped
}

func gatedLoops() (*procs.Loops, *gateClock) {
	table := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}, unstoppable: map[int32]bool{100: true}}
	gate := &gateClock{now: time.Unix(1_800_000_000, 0), in: make(chan struct{}), out: make(chan struct{})}
	return procs.New(table, table, gate, slog.New(slog.DiscardHandler)), gate
}

func wantBlocked[T any](t *testing.T, ch <-chan T, msg string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal(msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// gateClock blocks the first Sleep until out is closed.
type gateClock struct {
	fakeClock

	once    sync.Once
	in, out chan struct{}
}

func (c *gateClock) Sleep(ctx context.Context, d time.Duration) {
	c.once.Do(func() { close(c.in); <-c.out })
	c.fakeClock.Sleep(ctx, d)
}
