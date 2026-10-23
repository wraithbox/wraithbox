// Command wb-hostd is the per-user host daemon: sessions, policy, approvals,
// audit and the git gateway, on every host OS. VMs themselves are run by the
// platform's wb-vmd. See docs/spec/S04-architecture/S04-00-architecture.md.
//
// Today it runs the VM manager with wb-vmd as its provider, and serves
// wraithbox.hostd.v1 on the local IPC endpoint. It starts no VM at
// start, and on SIGTERM it closes every wb-vmd's lifeline and exits
// (S04-07-supervision, "When `wb-hostd` stops").
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

func main() {
	// Of S04-03-self-sandboxing, "`wb-hostd` before it confines itself",
	// this is what exists today: GOMAXPROCS here, and its configuration
	// in run. A fixed value also stops the runtime from changing it
	// later. The profile comes with M6.1.
	runtime.GOMAXPROCS(runtime.NumCPU())
	if err := daemon(); err != nil {
		fmt.Fprintf(os.Stderr, "wb-hostd: %v\n", err)
		os.Exit(1)
	}
}

// daemon runs wb-hostd for the effective user until SIGTERM.
func daemon() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	p, err := paths.Current(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("its own path: %w", err)
	}
	return run(ctx, options{Paths: p, Executable: exe, Version: version.Build()})
}
