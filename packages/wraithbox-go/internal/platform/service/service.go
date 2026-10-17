// Package service is the service manager of S12-01-platform-interfaces,
// "Service manager": it runs wb-hostd per user, at login and again
// whenever it exits. `wb setup` installs it (S17-02-setup).
//
// On macOS the service is the LaunchAgent nl.wraithbox.hostd, run by
// /bin/launchctl. Linux and Windows return an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
//
// The commands run through a Runner, so the tests record each argument
// list and environment instead of loading an agent on the host.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"time"
)

// Label is the service's name: the LaunchAgent label on macOS. It is
// fixed, so a user has one install (S17-02-setup, "The agent").
const Label = "nl.wraithbox.hostd"

// State is what the service manager has of the service now.
type State struct {
	// Current is whether the definition on disk has exactly the bytes
	// that Replace would write for the same program.
	Current bool
	// Loaded is whether the service manager has the service loaded.
	Loaded bool
}

// Manager installs and starts wb-hostd as the user's service. program is
// the absolute real path of the wb-hostd to run.
type Manager interface {
	// State compares the definition on disk with the one for program,
	// and asks the service manager whether the service is loaded. It
	// changes nothing.
	State(ctx context.Context, program string) (State, error)
	// Replace writes the definition for program over the one on disk,
	// stops the service when it is loaded, and starts it from the new
	// definition. Stopping wb-hostd stops its VMs, so the caller first
	// makes sure none runs (S17-02-setup, "The agent").
	Replace(ctx context.Context, program string) error
	// Start starts the service from the definition on disk, when it
	// isn't loaded.
	Start(ctx context.Context) error
}

// Config is what New needs. Each field comes from the caller, so the
// tests run in temporary directories with a recording Runner.
type Config struct {
	// Home is the user's home directory, from the user database.
	Home string
	// UID is the user's ID, for the launchd domain gui/<uid>.
	UID int
	// Run runs the service manager's commands. Nil is Exec.
	Run Runner
	// Log gets each command and its result. Nil is slog.Default().
	Log *slog.Logger
}

// Command is one run of a program: an absolute path, its arguments
// without the program name, and its whole environment.
type Command struct {
	Path string
	Args []string
	Env  []string
}

// Result is how a command ended: its exit code, and the first
// maxOutput bytes of its standard output and standard error together.
type Result struct {
	Code   int
	Output []byte
}

// Runner runs a command and waits for it. Its error is for a command
// that couldn't run or didn't end in time. A command that ran and exited
// with a nonzero code is a Result, not an error.
type Runner func(ctx context.Context, c Command) (Result, error)

const (
	// runTimeout bounds one command (SEC13-bounded-resources).
	runTimeout = 30 * time.Second
	// maxOutput bounds what is kept of a command's output.
	maxOutput = 16 << 10
)

// Exec is the Runner that runs the command with os/exec. The command's
// environment is c.Env and nothing else, never the caller's. A relative
// path is refused, so nothing is looked up through PATH
// (SEC03-no-host-exec).
func Exec(ctx context.Context, c Command) (Result, error) {
	if !filepath.IsAbs(c.Path) {
		return Result{}, fmt.Errorf("service: run %q: not an absolute path", c.Path)
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	out := &cappedBuffer{limit: maxOutput}
	cmd := exec.CommandContext(ctx, c.Path, c.Args...) //nolint:gosec // G204: the callers pass fixed absolute paths such as /bin/launchctl, checked above, and no shell parses the arguments
	// A nil Env would hand on the caller's environment. An empty one
	// hands on nothing.
	cmd.Env = append([]string{}, c.Env...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("service: run %s: %w", c.Path, context.Cause(ctx))
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return Result{Code: ee.ExitCode(), Output: out.buf}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("service: run %s: %w", c.Path, err)
	}
	return Result{Output: out.buf}, nil
}

// cappedBuffer keeps the first limit bytes written to it and drops the
// rest, so a child process can't make the caller hold unbounded output.
type cappedBuffer struct {
	buf   []byte
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - len(c.buf)
	c.buf = append(c.buf, p[:min(len(p), max(room, 0))]...)
	return len(p), nil
}
