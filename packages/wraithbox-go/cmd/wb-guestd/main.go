// Command wb-guestd is the agent that runs inside the guest VM, as root.
// It serves GuestService to wb-hostd on the host-guest socket. See
// docs/spec/S06-vm-lifecycle/S06-12-guest-agent.md.
//
// It logs one JSON object per line on standard error, as wb-vmd does.
// The service manager sends standard error to a file in the guest.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	own := version.Build()
	log.LogAttrs(ctx, slog.LevelInfo, "starting", slog.String("version", version.Label(own)))

	lis, err := guestsock.Listen(ctx, log, guestsock.Port)
	if errors.Is(err, context.Canceled) {
		return 0 // stopped while the port was still in use
	}
	if err != nil {
		return 1 // Listen logged the errno and the rule
	}

	srv := guestd.NewServer(own, log)
	go func() {
		<-ctx.Done()
		srv.Stop()
	}()
	if err := srv.Serve(lis); err != nil {
		log.LogAttrs(ctx, slog.LevelError, "serve failed", slog.String("error", err.Error()))
		return 1
	}
	log.LogAttrs(ctx, slog.LevelInfo, "stopped")
	return 0
}
