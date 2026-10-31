// Package projectcmd is `wb project` (S05-03-management-commands):
// `show`, `confirm`, `move`, `rename`, `place` and `rm`. Each resolves
// its project from the directory, the current one or `-C`, with wb's
// own git (S05-04-naming, "Which commands"), or takes it from
// `--project`, and asks wb-hostd's ProjectService for the records.
// wb-hostd writes the 5019 record of each change.
package projectcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostgit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/relay"
)

// The exit statuses (S05-02-exit-status-and-signals, "Exit status").
const (
	exitOK      = 0
	exitUsage   = 2
	exitFailure = 255
)

// The rules of wb's own refusals in this package.
const (
	// RuleUnavailable: wb can't find its paths.
	RuleUnavailable = "project-paths-unavailable"
	// RuleUnreachable: wb-hostd can't be reached, or answered without a
	// rule.
	RuleUnreachable = "hostd-unreachable"
)

// Hostd is wb-hostd's ProjectService on one connection.
type Hostd interface {
	hostdv1.ProjectServiceClient
	Close() error
}

// Env is what the command runs with. SystemEnv builds wb's.
type Env struct {
	Stdout, Stderr io.Writer
	// Paths are wb's <config>, <data> and <logs>.
	Paths paths.Paths
	// Unavailable is set when the paths can't be found.
	Unavailable error
	// Dial connects to wb-hostd.
	Dial func(ctx context.Context) (Hostd, error)
	Log  *slog.Logger

	// The rest is `wb shell`'s. DialShell connects to wb-hostd, Terminal
	// is relay.System, and Lookup reads wb's environment.
	DialShell func(ctx context.Context) (ShellHostd, error)
	Terminal  func() (cfg relay.Config, stop func(), err error)
	Lookup    func(name string) (string, bool)
}

// Run runs `wb project <args>` in dir, the directory of `-C` or the
// current one, and returns the exit status.
func Run(ctx context.Context, env Env, dir string, args []string) int {
	inv, err := parse(args)
	if err != nil {
		return usage(env, err.Error())
	}
	if env.Unavailable != nil {
		return fail(env, RuleUnavailable, "wb can't find its paths: "+env.Unavailable.Error())
	}
	hostd, err := env.Dial(ctx)
	if err != nil {
		return fail(env, RuleUnreachable, "wb-hostd can't be reached: "+err.Error())
	}
	defer func() { _ = hostd.Close() }()
	c := &cmd{
		env: env, dir: dir, inv: inv, hostd: hostd,
		open: &project.Opener{Paths: env.Paths, Hostd: hostd, Log: env.Log},
	}
	if err := c.run(ctx); err != nil {
		return c.report(err)
	}
	return exitOK
}

// cmd is one run of a subcommand.
type cmd struct {
	env   Env
	dir   string
	inv   invocation
	hostd Hostd
	open  *project.Opener
}

func (c *cmd) run(ctx context.Context) error {
	switch c.inv.sub {
	case "show":
		return c.show(ctx)
	case "confirm":
		return c.confirm(ctx)
	case "move":
		return c.move(ctx)
	case "rename":
		return c.rename(ctx)
	case "place":
		return c.place(ctx)
	}
	return c.rm(ctx)
}

const usageText = `Usage: wb project show [--project P]
       wb project confirm
       wb project move <dir> [--project P]
       wb project rename <name> [--project P]
       wb project place work|isolated [--project P]
       wb project rm [--project P]

P is a project id or name. Without --project, the project is the one of
the repository in the current directory, or in the directory of -C.
`

func usage(env Env, msg string) int {
	fmt.Fprintf(env.Stderr, "wb: %s\n\n%s", printable(msg), usageText)
	return exitUsage
}

// fail prints the last line of a failure, `wraith box: <message>
// (<rule>)`, and returns 255.
func fail(env Env, rule, msg string) int {
	fmt.Fprintf(env.Stderr, "wraith box: %s (%s)\n", printable(msg), printable(rule))
	return exitFailure
}

// report prints a refusal and returns 255. A refused identity check
// first lists what was recorded and what is there now
// (NFR06-explained-refusals).
func (c *cmd) report(err error) int {
	if m := project.Mismatch(err); m != nil {
		printIdentity(c.env.Stderr, "recorded", m.GetRecorded())
		printIdentity(c.env.Stderr, "now", m.GetCurrent())
	}
	rule, msg := failureOf(err)
	return fail(c.env, rule, msg)
}

// failureOf reads the rule and the message of an error: a refusal of
// wb's own, or of wb-hostd, whose ErrorInfo reason is the rule. An
// error without a rule is RuleUnreachable.
func failureOf(err error) (string, string) {
	if r, ok := errors.AsType[*project.Refusal](err); ok {
		return string(r.Rule), r.Error()
	}
	if r, ok := errors.AsType[*hostgit.Refusal](err); ok {
		return string(r.Rule), r.Error()
	}
	if r, ok := errors.AsType[*config.Refusal](err); ok {
		return string(r.Rule), r.Error()
	}
	if r, ok := errors.AsType[*versionError](err); ok {
		return r.rule, r.msg
	}
	st, ok := status.FromError(err)
	if !ok {
		return RuleUnreachable, err.Error()
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetReason() != "" {
			return info.GetReason(), st.Message()
		}
	}
	return RuleUnreachable, "wb-hostd failed: " + st.Message()
}

// versionError is a build version refusal of the connection, with both
// versions in its message (S04-08-records-and-bounds, "Between host
// processes").
type versionError struct {
	rule, msg string
}

func (v *versionError) Error() string { return v.msg }

// maxPrinted bounds one printed line.
const maxPrinted = 2048

// printable removes the escaped set of S16-03-around-the-stream from s,
// newlines included, then cuts it. Every value from the repository or
// from wb-hostd goes through it before it is printed, so a remote URL
// or a name can't rewrite the terminal (SEC14-no-fake-approvals).
func printable(s string) string {
	s = guesttext.Strip(s)
	if len(s) > maxPrinted {
		s = strings.ToValidUTF8(s[:maxPrinted], "") + " [cut]"
	}
	return s
}

// line prints one line of label and value, the value made printable.
func line(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%s: %s\n", label, printable(value))
}
