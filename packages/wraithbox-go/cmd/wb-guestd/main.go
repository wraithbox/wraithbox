// Command wb-guestd is the agent that runs inside the guest VM, as root.
// It serves GuestService to wb-hostd on the host-guest socket. See
// docs/spec/S06-vm-lifecycle/S06-12-guest-agent.md.
//
// Before it listens, it updates itself from the guest tools disk when
// the disk's version isn't its own (internal/guestupdate), and runs the
// installed wb-guestd in its place. Then it runs the stop loop, so no
// project user's process runs on while no host is connected
// (S13-01-process-hardening, "When the link drops"). It closes cron and
// at to every user but root, and removes every crontab and at job a
// project user left (S13-01-process-hardening, "Cron and at").
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
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/shell"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestupdate"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsched"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	// wb-guestd starts its own executable with this one argument as a
	// shell's session helper, as the project user
	// (S06-09-session-lifecycle, "Session helper"). It is checked before
	// any other start-up code, so the helper never updates, listens or
	// stops processes.
	if len(os.Args) == 2 && os.Args[1] == shell.HelperArg {
		os.Exit(shell.HelperMain())
	}
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

	// A wb-guestd that starts again, after a crash or a kill, finds the
	// project users' processes of the last link still running
	// (S13-01-process-hardening, "When the link drops";
	// SEC08-proj-isolation).
	loops := guestd.SystemProcesses(log)
	loops.StopAll(ctx, "start")

	// cron and atrun run as root and start a user's jobs outside the
	// kill loop's reach. So no project user may add one, and none may
	// keep one from before. A failure exits before any project user is
	// made or unlocked (S13-01-process-hardening, "Cron and at";
	// SEC08-proj-isolation).
	if !guestd.LockSchedulers(ctx, log, guestsched.Lock) ||
		!guestd.SweepSchedulers(ctx, log, guestsched.Sweep) {
		return 1
	}

	lis, err := guestsock.Listen(ctx, log, guestsock.Port)
	if errors.Is(err, context.Canceled) {
		return 0 // stopped while the port was still in use
	}
	if err != nil {
		return 1 // Listen logged the errno and the rule
	}

	// The session helper is this executable, resolved once, after the
	// update at start ran the installed one.
	exe, err := os.Executable()
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "own executable not found", slog.String("error", err.Error()))
		return 1
	}
	srv := guestd.NewServer(own, log, guestd.WithProcesses(loops), guestd.WithShellHelper(exe))
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
