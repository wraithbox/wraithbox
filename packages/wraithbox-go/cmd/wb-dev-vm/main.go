// Command wb-dev-vm starts wb-vmd with the test bundle in the work
// slot, boots it, and reaches the guest through wb-vmd's hand-off, so
// VM spikes and needs-vm work can talk to their guest code without
// rebuilding that each time. It is test tooling: never in the app
// bundle, and no product program links it (S10-tech-stack, "Test
// tooling").
//
//	wb-dev-vm boot [flags]                           # boot, print each state, stop on Ctrl-C
//	wb-dev-vm hello [-port N] [-hold] [-guest-version V] [flags]  # boot, run wb-guestd's hello, print its version, stop
//	wb-dev-vm connect -port N -listen PATH [flags]   # boot, relay each client of PATH to guest port N
//
// It starts wb-vmd as wb-launcher is to (S04-03-self-sandboxing, "How
// `wb-vmd` is started"): no arguments, the environment WB_VMD_SLOT=work
// and nothing else, and the descriptors 0 (lifeline), 2 (log), 3 (a
// listening gRPC socket in a new private directory) and 4 (the hand-off
// socketpair). Every descriptor wb-dev-vm opens is close-on-exec, but one
// its own parent left open without that flag passes on, as it would to
// any child. wb-vmd confines itself to the work slot's bundle,
// <data>/vms/macos-work.noindex, and the slot checks refuse any other
// (S04-04-vm-processes, "Slot checks"). The bundle has to be there
// before wb-dev-vm runs. It runs one VM at a time, so a needs-vm builder
// that uses it stays within NFR05-two-macos-vms.
//
// wb-vmd refuses a peer of another build version, so wb-dev-vm has to
// be built with the same stamp as wb-vmd and wb-guestd:
// `mise run go:build ::: swift:sign` builds bin/wb-dev-vm, bin/wb-guestd and
// the signed wb-vmd with one stamp. A test guest outlives the build that
// put wb-guestd in it, so `hello -guest-version` names the version
// wb-guestd has to report. The hello checks it as wb-hostd does.
//
// Spike guest daemons listen on a guest port below 1024, as wb-guestd
// does, because wb-vmd refuses a ConnectGuest to any other
// (S04-02-vm-provider, "Guest ports below 1024").
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = "usage: wb-dev-vm boot | hello | connect [flags] (see the package comment, or wb-dev-vm <command> -h)"

// errUsage is a usage error: exit 2.
var errUsage = errors.New(usage)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	err := dispatch(ctx, args, stdin, stdout, stderr)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 2
	case errors.Is(err, errUsage):
		fmt.Fprintln(stderr, usage)
		return 2
	default:
		fmt.Fprintf(stderr, "wb-dev-vm: FAIL: %v\n", err)
		return 1
	}
}

func dispatch(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	rest := args[1:]
	switch args[0] {
	case "boot":
		return bootCommand(ctx, rest, stdout, stderr)
	case "hello":
		return helloCommand(ctx, rest, stdin, stdout, stderr)
	case "connect":
		return connectCommand(ctx, rest, stdout, stderr)
	}
	return fmt.Errorf("unknown command %q: %w", args[0], errUsage)
}
