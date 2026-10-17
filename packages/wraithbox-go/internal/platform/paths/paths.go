// Package paths gives the host directories of S04-architecture, "Host
// state": <config>, <data>, <logs>, and <data>/run, the user-only
// directory for local IPC endpoints. Every program, wb included, gets
// them from the same source: the home directory in the user database
// for the effective user ID, never HOME or any other variable. So the
// paths don't depend on a program's environment, and a daemon reads no
// environment before it confines itself (S04-03-self-sandboxing,
// "Confined first thing in `main`"; S12-01-platform-interfaces, "Paths").
//
// It is in internal/platform because the roots differ per OS
// (S12-01-platform-interfaces, "Rules"). Linux and Windows return an
// error that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly"). The subpaths of <data> other
// than run/ are added by the packages that use them.
package paths

import (
	"context"
	"log/slog"
)

// Paths are the host directories of one user. Each is an absolute path.
// Current doesn't create them: `wb setup` does.
type Paths struct {
	// Config is <config>: config.toml and the policy files.
	Config string
	// Data is <data>: VM bundles, state.db, project repositories.
	Data string
	// Logs is <logs>: the audit log and the process logs.
	Logs string
	// Run is <data>/run, the user-only directory for local IPC
	// endpoints (internal/platform/localipc).
	Run string
}

// Rule names the rule behind a refusal, for the log.
type Rule string

// RuleHomeInvalid: the user database gives the effective user an empty
// or relative home directory, so no path can be built from it.
const RuleHomeInvalid Rule = "paths-home-invalid"

// Refusal is the error of a refusal in this package.
type Refusal struct {
	Rule Rule

	msg string
}

func (r *Refusal) Error() string { return "paths: " + string(r.Rule) + ": " + r.msg }

// logRefusal logs a refusal with its rule. A nil log is slog.Default().
func logRefusal(log *slog.Logger, r *Refusal, attrs ...slog.Attr) {
	if log == nil {
		log = slog.Default()
	}
	attrs = append([]slog.Attr{
		slog.String("rule", string(r.Rule)),
		slog.String("decision", "refuse"),
		slog.String("reason", r.msg),
	}, attrs...)
	log.LogAttrs(context.Background(), slog.LevelError, "host paths refused", attrs...)
}
