package procs_test

import (
	"bytes"
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

// TestIdleWaitsForALoop: Idle returns only once a running loop ends.
func TestIdleWaitsForALoop(t *testing.T) {
	table := &fakeTable{procs: []gp.Proc{proc(100, 10000, "zsh")}, unstoppable: map[int32]bool{100: true}}
	var buf bytes.Buffer
	gate := &gateClock{fakeClock: fakeClock{now: time.Unix(1_800_000_000, 0)}, in: make(chan struct{}), out: make(chan struct{})}
	l := procs.New(table, gate, slog.New(slog.NewJSONHandler(&buf, nil)))
	done := make(chan struct{})
	go func() { l.StopAll(context.Background(), "link-drop"); close(done) }()
	<-gate.in
	idle := make(chan struct{})
	go func() { l.Idle(); close(idle) }()
	select {
	case <-idle:
		t.Fatal("Idle returned while a loop ran")
	case <-time.After(50 * time.Millisecond):
	}
	close(gate.out)
	<-done
	<-idle
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
