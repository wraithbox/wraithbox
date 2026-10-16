package hostgit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// waitDelay bounds how long a killed git can hold its output pipes open,
// through a child that inherited them, before Wait returns.
const waitDelay = time.Second

// Command returns a command that runs `<path> -c core.hooksPath=/dev/null
// <args>` in dir, in a scrubbed environment (S05-04-naming, "Resolving
// the key"). The environment holds exactly:
//
//   - HOME, from the user database, never from $HOME;
//   - XDG_CONFIG_HOME, from the login environment, when it is set there
//     and is an absolute path;
//   - TMPDIR, the caller's temporary directory, which must be an
//     absolute path;
//   - LC_ALL=C.
//
// No PATH, and no GIT_* variable, so nothing in the caller's shell or a
// .envrc can point git at another repository or add configuration
// (SEC12-least-privilege). On Windows, os/exec also adds SYSTEMROOT
// when the environment lacks it.
//
// path must be absolute: Command never looks git up through PATH
// (SEC03-no-host-exec). On Windows, os/exec may add an extension from
// PATHEXT to an absolute path without one. It reads no PATH.
func Command(ctx context.Context, path, dir string, args ...string) (*exec.Cmd, error) {
	cmd, r := command(ctx, path, dir, args)
	if r != nil {
		logDecision(ctx, r.Result, r)
		return nil, r
	}
	return cmd, nil
}

// command is Command without the log line, for CheckVersion, which logs
// its own decision.
func command(ctx context.Context, path, dir string, args []string) (*exec.Cmd, *Refusal) {
	if !filepath.IsAbs(path) {
		return nil, pathNotAbsolute(path)
	}
	env, err := environment(ctx)
	if err != nil {
		return nil, &Refusal{
			Binary: path,
			Rule:   RuleEnvUnavailable,
			msg:    fmt.Sprintf("git at %s does not run: %v", path, err),
			err:    err,
		}
	}
	// An absolute path makes os/exec skip its PATH lookup.
	gitArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, path, gitArgs...)
	cmd.Dir = dir
	// A non-nil Env replaces the caller's environment. os/exec then adds
	// no PWD either.
	cmd.Env = env
	cmd.WaitDelay = waitDelay
	return cmd, nil
}

// environment builds the variables of Command. A variable that can't be
// worked out is an error, never a fallback to the caller's value
// (SEC12-least-privilege).
func environment(ctx context.Context) ([]string, error) {
	home, err := platform.UserHome()
	if err != nil {
		return nil, fmt.Errorf("HOME: %w", err)
	}
	// $TMPDIR, or the platform default when it is unset.
	tmp := os.TempDir()
	if !filepath.IsAbs(tmp) {
		return nil, fmt.Errorf("TMPDIR: %q is not an absolute path", tmp)
	}
	xdg, err := platform.LoginXDGConfigHome(ctx)
	if err != nil {
		return nil, fmt.Errorf("XDG_CONFIG_HOME from the login environment: %w", err)
	}
	env := []string{"HOME=" + home, "TMPDIR=" + tmp, "LC_ALL=C"}
	if xdg != "" {
		env = append(env, "XDG_CONFIG_HOME="+xdg)
	}
	return env, nil
}
