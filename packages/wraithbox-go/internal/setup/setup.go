// Package setup is `wb setup`: it checks the host and the bundle wb runs
// from, then installs the wb-hostd agent and the ~/.local/bin/wb link
// (S05-03-management-commands, "`wb setup`"; S17-02-setup). It needs no
// administrator rights, never edits a shell file, and replaces only
// files that are Wraith Box's own (SEC12-least-privilege).
//
// Everything it reads of the host comes in through Env, so the tests run
// in temporary directories with a fake service manager and a fake
// wb-hostd, and never touch the real ~/Library/LaunchAgents or
// ~/.local/bin.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/service"
)

// Exit statuses of Run.
const (
	ExitOK = 0
	// ExitRefused is the status of a refusal, of keychain-needs-team
	// after the install, and of any other failure.
	ExitRefused = 1
	// ExitUsage is the status of a usage error, as for every wb command.
	ExitUsage = 2
)

// Env is everything wb setup reads of the host and the program. SystemEnv
// gives the real one.
type Env struct {
	// Host is the host OS, from platform.Detect.
	Host platform.Host
	// ReadRelease reads the Mac's CPU and macOS version:
	// platform.ReadRelease.
	ReadRelease func() (platform.Release, error)
	// Home is the user's home directory, from the user database.
	Home string
	// UID is the user's ID, for the fix that names the launchd domain.
	UID int
	// Paths are <config>, <data>, <logs> and run (internal/platform/paths).
	Paths paths.Paths
	// PATH is the PATH wb runs with, for the hint when ~/.local/bin
	// isn't on it.
	PATH string
	// Executable is wb's own path, from os.Executable.
	Executable string
	// Version is wb's build version, version.Build(): "" when unstamped.
	Version string
	// SystemApplications is /Applications.
	SystemApplications string
	// Service is the service manager, which runs launchctl.
	Service service.Manager
	// Hostd asks the running wb-hostd which VMs run.
	Hostd Hostd
	// Unavailable is an error SystemEnv met building the fields above.
	// Run reports it after the host check, so a host that is later in
	// the matrix gets that refusal first.
	Unavailable error
	// Stdout gets what wb setup did, and Stderr each refusal with its
	// fix.
	Stdout io.Writer
	Stderr io.Writer
	// Log gets each decision with its rule. Nil is slog.Default().
	Log *slog.Logger
}

// Run runs `wb setup` with the arguments after the command name, and
// returns the exit status.
func Run(ctx context.Context, env Env, args []string) int {
	if env.Log == nil {
		env.Log = slog.Default()
	}
	if len(args) > 0 {
		if args[0] == "--remove" {
			fmt.Fprintln(env.Stderr, "wb setup: --remove is not built yet (S17-02-setup, \"Removal\")")
		} else {
			fmt.Fprintf(env.Stderr, "wb setup: takes no arguments, got %q\n", args)
		}
		return ExitUsage
	}
	err := install(ctx, &env)
	if err == nil {
		return ExitOK
	}
	if r, ok := errors.AsType[*Refusal](err); ok {
		fmt.Fprintf(env.Stderr, "wb setup: refused (%s): %s\n%s\n", r.Rule, r.Msg, r.Fix)
		return ExitRefused
	}
	fmt.Fprintf(env.Stderr, "wb setup: %v\n", err)
	return ExitRefused
}

// install runs the steps in the order of S17-02-setup: the host, the
// bundle and its location, then the link and the agent, all without
// writing anything. Only then does it write: the projects root, the
// agent, the link. So every refusal of a check leaves the host as it
// was. The Keychain check comes last, after the install.
func install(ctx context.Context, env *Env) error {
	if err := checkHost(ctx, env); err != nil {
		return err
	}
	if env.Unavailable != nil {
		return env.Unavailable
	}
	bundle, err := checkBundle(ctx, env)
	if err != nil {
		return err
	}
	l, err := planLink(ctx, env, bundle)
	if err != nil {
		return err
	}
	a, err := planAgent(ctx, env, bundle)
	if err != nil {
		return err
	}
	if err := makeDirs(env, env.Stdout); err != nil {
		return err
	}
	if err := applyAgent(ctx, env, a, env.Stdout); err != nil {
		return err
	}
	if err := applyLink(ctx, env, l, env.Stdout); err != nil {
		return err
	}
	if dir := filepath.Dir(l.path); !onPATH(env.PATH, dir) {
		fmt.Fprintf(env.Stdout, "wb setup: %s isn't on PATH. Add this line to your shell's startup file, such as ~/.zshrc:\n  %s\n", dir, pathLine)
	}
	return checkKeychain(ctx, env, bundle)
}

// pathLine is the line that puts ~/.local/bin on PATH. wb setup prints
// it, and never edits a shell file (S17-02-setup, "The link").
const pathLine = `export PATH="$HOME/.local/bin:$PATH"`
