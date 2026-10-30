package guestd

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/stats"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
)

// linkDropGrace is how long a shell whose stream ended waits for its
// connection's end. gRPC cancels a connection's streams before it
// reports the connection's end, so the shell can't tell a link drop
// from a stream the host ended until then.
const linkDropGrace = 2 * time.Second

// linkWatch counts the host's connections, and runs the stop loop when
// the last one ends: with the link down, no project user's process runs
// on (S13-01-process-hardening, "When the link drops";
// SEC08-proj-isolation). The loop runs on its own goroutine, so the
// connection's close doesn't wait for it. A new connection doesn't end
// it: the loop runs to its bounds, and a shell start waits for it
// (procs.Loops.HoldStops).
type linkWatch struct {
	loops *procs.Loops
	log   *slog.Logger
	// done, when set, gets each stop loop's result. Tests set it.
	done func(procs.StopResult)

	mu    sync.Mutex
	conns int
}

// connKey is the context key of a connection's connState.
type connKey struct{}

// connState is one connection from the host. ended is closed at its
// end.
type connState struct {
	ended chan struct{}
}

func (w *linkWatch) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

func (*linkWatch) HandleRPC(context.Context, stats.RPCStats) {}

// TagConn gives each connection a connState, which its streams' contexts
// carry.
func (w *linkWatch) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return context.WithValue(ctx, connKey{}, &connState{ended: make(chan struct{})})
}

// HandleConn counts a connection's begin and end. The last end starts
// a stop loop.
func (w *linkWatch) HandleConn(ctx context.Context, s stats.ConnStats) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch s.(type) {
	case *stats.ConnBegin:
		w.conns++
		return
	case *stats.ConnEnd:
		w.conns--
		if c, ok := ctx.Value(connKey{}).(*connState); ok {
			close(c.ended)
		}
	default:
		return
	}
	if w.conns > 0 {
		return
	}
	w.log.LogAttrs(ctx, slog.LevelWarn, "host link dropped", slog.String("decision", "stop-project-users"))
	loopCtx := context.WithoutCancel(ctx)
	go func() {
		res := w.loops.StopAll(loopCtx, "link-drop")
		if w.done != nil {
			w.done(res)
		}
	}()
}

// dropped reports whether the connection of a stream whose context is
// ctx ended within linkDropGrace with no other connection left: the
// link dropped, and the stop loop has the shell's processes. It reports
// false for a stream the host ended on a live connection, and for a ctx
// without a connection.
func (w *linkWatch) dropped(ctx context.Context) bool {
	c, ok := ctx.Value(connKey{}).(*connState)
	if !ok {
		return false
	}
	t := time.NewTimer(linkDropGrace)
	defer t.Stop()
	select {
	case <-c.ended:
	case <-t.C:
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conns == 0
}
