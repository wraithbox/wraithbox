package guestd

import (
	"context"
	"log/slog"
	"sync"

	"google.golang.org/grpc/stats"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
)

// linkWatch counts the host's connections, and runs the stop loop when
// the last one ends: with the link down, no project user's process runs
// on (S13-01-process-hardening, "When the link drops";
// SEC08-proj-isolation). The loop runs on its own goroutine, so the
// connection's close doesn't wait for it.
type linkWatch struct {
	loops *procs.Loops
	log   *slog.Logger
	// done, when set, gets each stop loop's result. Tests set it.
	done func(procs.StopResult)

	mu    sync.Mutex
	conns int
}

func (w *linkWatch) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

func (*linkWatch) HandleRPC(context.Context, stats.RPCStats) {}

func (w *linkWatch) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

// HandleConn counts a connection's begin and end, and starts the stop
// loop when no connection is left.
func (w *linkWatch) HandleConn(ctx context.Context, s stats.ConnStats) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch s.(type) {
	case *stats.ConnBegin:
		w.conns++
		return
	case *stats.ConnEnd:
		w.conns--
	default:
		return
	}
	if w.conns > 0 {
		return
	}
	w.log.LogAttrs(ctx, slog.LevelWarn, "host link dropped", slog.String("decision", "stop-project-users"))
	go func() {
		res := w.loops.StopAll(context.WithoutCancel(ctx), "link-drop")
		if w.done != nil {
			w.done(res)
		}
	}()
}
