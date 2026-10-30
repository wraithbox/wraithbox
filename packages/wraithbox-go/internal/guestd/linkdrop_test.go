package guestd

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/stats"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
)

// stubbornTable lists one process in the project range that SIGSTOP
// never stops, so a stop loop runs until a bound or its ctx ends. stops
// gets one value per SIGSTOP sent.
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

// TestLinkWatchCancelsOnBegin: a new connection cancels the link-drop
// stop loop that still runs, and the loop reports it was cut short.
func TestLinkWatchCancelsOnBegin(t *testing.T) {
	table := &stubbornTable{stops: make(chan struct{})}
	results := make(chan procs.StopResult, 1)
	w := &linkWatch{
		loops: procs.New(table, procs.SystemClock{}, slog.New(slog.DiscardHandler)),
		log:   slog.New(slog.DiscardHandler),
		done:  func(r procs.StopResult) { results <- r },
	}
	c1 := w.TagConn(context.Background(), &stats.ConnTagInfo{})
	w.HandleConn(c1, &stats.ConnBegin{})
	w.HandleConn(c1, &stats.ConnEnd{})
	select {
	case <-table.stops:
	case <-time.After(10 * time.Second):
		t.Fatal("the stop loop sent no SIGSTOP")
	}
	c2 := w.TagConn(context.Background(), &stats.ConnTagInfo{})
	w.HandleConn(c2, &stats.ConnBegin{})
	select {
	case r := <-results:
		if !r.CutShort || r.Rounds >= procs.MaxRounds {
			t.Errorf("result %+v, want cut short", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stop loop ran on after a new connection")
	}
	w.loops.Idle()
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
	w.HandleConn(w.TagConn(context.Background(), &stats.ConnTagInfo{}), &stats.ConnBegin{})
	<-results
}
