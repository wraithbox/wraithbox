package hostgit

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	out := &cappedBuffer{limit: versionMaxOutput}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return Result{}, runFailed(ctx, path, err)
	}

	line, ok := firstLine(out.buf, out.over)
	v, readable := parseVersionLine(line)
	if !ok || !readable {
		return Result{}, unreadable(path, line)
	}
	text := strings.TrimPrefix(line, "git version ")
	if v.less(minimum) {
		return Result{}, &Refusal{
			Binary:  path,
			Version: text,
			Rule:    RuleVersionTooOld,
			msg:     fmt.Sprintf("git at %s is version %s, older than the minimum %s", path, text, MinVersion),
		}
	}
	return Result{Binary: path, Version: text, Rule: RuleVersionAccepted}, nil
}

// runFailed explains a run that failed or timed out. When the binary is
// the platform default, it adds how to install git or pick another one
// (S08-08-host-git, "Which git").
func runFailed(ctx context.Context, path string, err error) *Refusal {
	cause := err.Error()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		cause = "it did not finish before the deadline"
	}
	msg := fmt.Sprintf("git at %s did not run `git version` (%s). Wraith Box needs git %s or later", path, cause, MinVersion)
	if path == platform.DefaultGitPath() {
		msg += ". To fix this, " + platform.GitInstallHint()
	}
	return &Refusal{
		Binary: path,
		Rule:   RuleRunFailed,
		msg:    msg,
		err:    err,
	}
}

func unreadable(path, line string) *Refusal {
	shown := line
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	return &Refusal{
		Binary:  path,
		Version: shown,
		Rule:    RuleVersionUnreadable,
		msg: fmt.Sprintf("git at %s printed a version that can't be read: %q. Wraith Box needs git %s or later",
			path, shown, MinVersion),
	}
}

// cappedBuffer keeps the first limit bytes written to it and drops the
// rest, so git can't make the caller hold unbounded output.
type cappedBuffer struct {
	buf   []byte
	limit int
	over  bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - len(c.buf)
	if len(p) > room {
		c.over = true
		c.buf = append(c.buf, p[:room]...)
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}
