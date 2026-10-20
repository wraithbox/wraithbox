package hostgit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

const (
	// versionTimeout is the deadline of one `git version` run
	// (S08-08-host-git, "Where it is checked").
	versionTimeout = 10 * time.Second
	// versionMaxOutput bounds what is kept of the output. The first line
	// must end within it.
	versionMaxOutput = 1024
	// maxShown bounds the unreadable output a message quotes.
	maxShown = 80
)

// CheckVersion runs `<path> version` in the environment of Command, under
// a deadline, and checks that the version is at least MinVersion
// (S08-08-host-git, "Where it is checked"). A failed run, output it
// can't read, and a version below the minimum are each refused with
// their own rule. The decision is logged, and the Result carries the
// binary, the version it read and the rule.
func CheckVersion(ctx context.Context, path string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	res, r := checkVersion(ctx, path)
	if r != nil {
		logDecision(ctx, r.Result, r)
		return r.Result, r
	}
	logDecision(ctx, res, nil)
	return res, nil
}

func checkVersion(ctx context.Context, path string) (Result, *Refusal) {
	cmd, r := command(ctx, path, "", []string{"version"})
	if r != nil {
		return Result{}, r
	}
	out := NewCappedBuffer(versionMaxOutput)
	stderr := NewCappedBuffer(maxShown)
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return Result{}, runFailed(ctx, path, err, stderr.Bytes())
	}

	line, ok := firstLine(out.Bytes(), out.Over())
	v, readable := parseVersionLine(line)
	if !ok || !readable {
		return Result{}, unreadable(path, line)
	}
	if v.less(minimum) {
		return Result{}, &Refusal{
			Binary:  path,
			Version: v.String(),
			Rule:    RuleVersionTooOld,
			msg:     fmt.Sprintf("git at %s is version %s, older than the minimum %s", path, v, MinVersion),
		}
	}
	return Result{Binary: path, Version: v.String(), Rule: RuleVersionAccepted}, nil
}

// runFailed explains a run that failed, with an excerpt of what git
// wrote to stderr (NFR06-explained-refusals). When the binary is the
// platform default and it failed on its own, it adds how to install git
// or pick another one (S08-08-host-git, "Which git"). A run the context
// ended says so, without the hint.
func runFailed(ctx context.Context, path string, err error, stderr []byte) *Refusal {
	r := &Refusal{
		Binary: path,
		Rule:   RuleRunFailed,
		err:    err,
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		r.msg = fmt.Sprintf("the check of git at %s timed out before `git version` finished. Wraith Box needs git %s or later",
			path, MinVersion)
		return r
	case ctx.Err() != nil:
		r.msg = fmt.Sprintf("the check of git at %s was canceled before `git version` finished. Wraith Box needs git %s or later",
			path, MinVersion)
		return r
	}
	r.msg = fmt.Sprintf("git at %s did not run `git version` (%v). Wraith Box needs git %s or later", path, err, MinVersion)
	if len(stderr) > 0 {
		r.msg += fmt.Sprintf(". It printed %q", stderr)
	}
	if path == platform.DefaultGitPath() {
		r.msg += ". To fix this, " + platform.GitInstallHint()
	}
	return r
}

// unreadable quotes the start of the line in the message only. %q
// escapes its control bytes.
func unreadable(path, line string) *Refusal {
	shown := line
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	return &Refusal{
		Binary: path,
		Rule:   RuleVersionUnreadable,
		msg: fmt.Sprintf("git at %s printed a version that can't be read: %q. Wraith Box needs git %s or later",
			path, shown, MinVersion),
	}
}
