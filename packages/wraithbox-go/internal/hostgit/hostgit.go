// Package hostgit resolves the host's git binary, checks its version, and
// builds the commands that run it in a scrubbed environment. wb and
// wb-hostd both use it (S08-08-host-git, and S05-04-naming, "Resolving
// the key").
//
// Nothing here looks a program up through PATH or reads PATH
// (SEC03-no-host-exec), and no variable of the caller's environment
// reaches git except TMPDIR, and on Windows the SYSTEMROOT that os/exec
// adds (SEC12-least-privilege).
package hostgit

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// MinVersion is the oldest git Wraith Box runs on the host: the oldest
// version the git spikes ran, X07-git-round-trip among them
// (S08-08-host-git, "Minimum version").
const MinVersion = "2.54.0"

// Rule names the rule behind a decision, for the log and later the audit
// log (S08-08-host-git, "Where it is checked").
type Rule string

// The rules of this package. Each refusal has its own.
const (
	// RuleVersionAccepted: git ran and its version is at least MinVersion.
	RuleVersionAccepted Rule = "git-version-accepted"
	// RulePathNotAbsolute: the git setting, or a path given to Command,
	// is not an absolute path.
	RulePathNotAbsolute Rule = "git-path-not-absolute"
	// RuleEnvUnavailable: a variable of the scrubbed environment could
	// not be worked out, so git doesn't run.
	RuleEnvUnavailable Rule = "git-env-unavailable"
	// RuleRunFailed: `git version` failed to start, exited nonzero, or
	// didn't finish before the deadline.
	RuleRunFailed Rule = "git-run-failed"
	// RuleVersionUnreadable: the output isn't `git version` and a version
	// in the form S08-08-host-git accepts.
	RuleVersionUnreadable Rule = "git-version-unreadable"
	// RuleVersionTooOld: the version is below MinVersion.
	RuleVersionTooOld Rule = "git-version-too-old"
)

// Result is the outcome of a check: the binary, the version it printed
// (empty when none was read), and the rule that decided.
type Result struct {
	Binary  string
	Version string
	Rule    Rule
}

// Refusal is the error of every refusal in this package. Its message
// names the binary, the version when one was read, and the minimum
// (NFR06-explained-refusals).
type Refusal struct {
	Result

	msg string
	err error
}

func (r *Refusal) Error() string { return r.msg }

// Unwrap returns the cause, such as the error of a failed run.
func (r *Refusal) Unwrap() error { return r.err }

// Resolve returns the absolute path of the git binary to run. setting is
// the git key of config.toml, which the caller reads. An empty setting
// gives the platform's fixed default. A setting that is not an absolute
// path is refused (S08-08-host-git, "Which git"). It never looks git up
// through PATH (SEC03-no-host-exec).
func Resolve(setting string) (string, error) {
	if setting == "" {
		return platform.DefaultGitPath(), nil
	}
	if !filepath.IsAbs(setting) {
		r := pathNotAbsolute(setting)
		logDecision(context.Background(), r.Result, r)
		return "", r
	}
	return setting, nil
}

func pathNotAbsolute(path string) *Refusal {
	return &Refusal{
		Binary: path,
		Rule:   RulePathNotAbsolute,
		msg: fmt.Sprintf("the git binary %q is not an absolute path. Set git in config.toml to an absolute path, such as %s",
			path, platform.DefaultGitPath()),
	}
}

// logDecision logs a decision with the rule that made it. The caller
// sends it to the audit writer later (M3.10).
func logDecision(ctx context.Context, res Result, err error) {
	attrs := []slog.Attr{
		slog.String("rule", string(res.Rule)),
		slog.String("binary", res.Binary),
		slog.String("version", res.Version),
		slog.String("minimum", MinVersion),
	}
	if err != nil {
		attrs = append(attrs, slog.String("decision", "refuse"), slog.String("reason", err.Error()))
		slog.Default().LogAttrs(ctx, slog.LevelWarn, "host git refused", attrs...)
		return
	}
	attrs = append(attrs, slog.String("decision", "accept"))
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "host git accepted", attrs...)
}
