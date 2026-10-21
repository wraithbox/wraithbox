// Command wb-guestd is the agent that runs inside the guest VM, as root.
// It serves GuestService to wb-hostd on the host-guest socket. See
// docs/spec/S06-vm-lifecycle/S06-12-guest-agent.md.
//
// Before it listens, it updates itself from the guest tools disk when
// the disk's version isn't its own (internal/guestupdate), and runs the
// installed wb-guestd in its place.
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
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestupdate"
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
	// The process ID shows that an update kept it (S06-12-guest-agent,
	// "Update at start").
	log.LogAttrs(ctx, slog.LevelInfo, "starting",
		slog.String("version", version.Label(own)), slog.Int("pid", os.Getpid()))

	if err := guestupdate.AtStart(ctx, guestupdate.Config{Own: own, Log: log}); err != nil {
		if ctx.Err() != nil {
			return 0 // stopped while it waited for the tools disk
		}
		return 1 // AtStart logged it: the installed wb-guestd didn't run
	}

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
	// A signal between Listen and Serve stops the server before it serves,
	// and Serve then returns grpc.ErrServerStopped: a clean stop too.
	if err := srv.Serve(lis); err != nil && ctx.Err() == nil {
		log.LogAttrs(ctx, slog.LevelError, "serve failed", slog.String("error", err.Error()))
		return 1
	}
	log.LogAttrs(ctx, slog.LevelInfo, "stopped")
	return 0
}
