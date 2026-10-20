package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
)

const (
	// gitTimeout is the deadline of one git run that reads a few paths
	// or names (SEC13-bounded-resources).
	gitTimeout = 10 * time.Second
	// revListTimeout is the deadline of the root commit walk, which
	// reads the whole history (S05-04-naming, "Recorded identity").
	revListTimeout = 60 * time.Second
	// maxStderr bounds what is kept of git's error output.
	maxStderr = 1024
	// maxShown bounds the part of git's first error line that a message
	// quotes.
	maxShown = 200
)

// gitRun is one git run in dir, with its deadline and the cap on its
// output.
type gitRun struct {
	dir     string
	timeout time.Duration
	limit   int
	args    []string
}

// run runs git through hostgit.Command, so with no hooks and in the
// scrubbed environment of S05-04-naming, "Resolving the key"
// (SEC03-no-host-exec). It reads at most limit bytes of output: more is
// refused with RuleGitOutput (SEC13-bounded-resources). A nonzero exit
// or a missed deadline is refused with RuleGitFailed and git's first
// error line, escaped and cut.
//
// core.fsmonitor is off as well: it names a program that git runs when
// it reads the index, and a downloaded repository's configuration can
// set it. None of the commands here reads the index, so this only
// guards against a later change.
func (r *Resolver) run(ctx context.Context, g gitRun) ([]byte, *Refusal) {
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	args := append([]string{"-c", "core.fsmonitor=false"}, g.args...)
	cmd, err := hostgit.Command(ctx, r.Git, g.dir, args...)
	if err != nil {
		return nil, &Refusal{
			Rule: RuleGitFailed, err: err,
			msg: fmt.Sprintf("git %s does not run in %q: %v", g.args[0], g.dir, err),
		}
	}
	cmd.Env = append(cmd.Env, r.testEnv...)
	out := hostgit.NewCappedBuffer(g.limit)
	stderr := hostgit.NewCappedBuffer(maxStderr)
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, runFailed(ctx, g, err, stderr.Bytes())
	}
	if out.Over() {
		return nil, &Refusal{
			Rule: RuleGitOutput,
			msg:  fmt.Sprintf("git %s in %q printed more than %d bytes", g.args[0], g.dir, g.limit),
		}
	}
	return out.Bytes(), nil
}

// runFailed explains a failed run with git's first error line.
func runFailed(ctx context.Context, g gitRun, err error, stderr []byte) *Refusal {
	r := &Refusal{Rule: RuleGitFailed, err: err}
	if ctx.Err() != nil {
		r.msg = fmt.Sprintf("git %s in %q did not finish within %s", g.args[0], g.dir, g.timeout)
		return r
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		r.exitCode = exit.ExitCode()
	}
	r.msg = fmt.Sprintf("git %s in %q failed (%v)", g.args[0], g.dir, err)
	if line := firstLine(stderr); line != "" {
		r.msg += ": " + line
	}
	return r
}

// firstLine returns the first line of b, cut to maxShown bytes and
// quoted, so its control bytes print escaped.
func firstLine(b []byte) string {
	line, _, _ := bytes.Cut(b, []byte("\n"))
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return ""
	}
	if len(line) > maxShown {
		line = line[:maxShown]
	}
	return strconv.Quote(string(line))
}

// hasControl reports whether s holds a control character (C0, DEL or
// C1) or isn't valid UTF-8. A path or name like that is refused rather
// than printed or passed on.
func hasControl(s string) bool {
	return !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl)
}
