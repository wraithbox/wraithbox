// Package credcmd is `wb cred` (S05-03-management-commands, "`wb cred`"):
// `set`, `list` and `rm` of the binding secrets in the secret store. wb
// holds no store access (S15-02-secret-store, "`wb` doesn't hold a
// group"), so each command runs wb-credset from wb's own bundle for one
// operation, and reports its outcome. For `set`, wb reads the value from
// a prompt or its standard input, writes it to wb-credset's standard
// input, and zeroes its buffer when that write ends (S15-03-access-rules,
// rule 5). It never fetches a value.
package credcmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/credset"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// The exit statuses (S05-02-exit-status-and-signals, "Exit status").
const (
	exitOK      = 0
	exitUsage   = 2
	exitFailure = 255
)

// The rules of wb's own refusals in `wb cred`. A refusal by wb-credset
// or the store is reported with its own rule.
const (
	// RuleHostLater: the host is later in the platform matrix
	// (S12-01-platform-interfaces, "Unimplemented platforms fail
	// clearly").
	RuleHostLater = "cred-host-later"
	// RuleNameInvalid: the binding name isn't 1 to 64 characters of a-z,
	// 0-9 and - (S15-03-access-rules, "Declared access").
	RuleNameInvalid = "cred-name-invalid"
	// RuleNoBundle: wb doesn't run from an installed bundle, so it has no
	// wb-credset to start.
	RuleNoBundle = "cred-no-bundle"
	// RuleValueRefused: the value is empty, or longer than
	// credset.MaxValue, or can't be read.
	RuleValueRefused = "cred-value-refused"
	// RuleHelperFailed: wb-credset couldn't be started, or exited without
	// a decision.
	RuleHelperFailed = "cred-helper-failed"
	// RuleNoCore: wb can't turn its core files off before it reads the
	// value.
	RuleNoCore = "cred-no-core"
	// RuleHelperSignaled: wb-credset ended on a signal.
	RuleHelperSignaled = "cred-helper-signaled"
)

// Env is what the command runs with. SystemEnv builds wb's.
type Env struct {
	Host platform.Host
	// Executable is wb's own path, from which it finds its bundle.
	Executable string
	// Stdin is wb's standard input, and StdinTerminal whether it is a
	// terminal. ReadHidden reads one line from that terminal with echo
	// off, without the line end.
	Stdin         io.Reader
	StdinTerminal bool
	ReadHidden    func() ([]byte, error)
	Stdout        io.Writer
	Stderr        io.Writer
	// LookupEnv reads wb's environment, for the variables of
	// credset.Environ.
	LookupEnv func(string) (string, bool)
	// ReadTeam reads the team of a code signature
	// (internal/platform/codesign).
	ReadTeam func(ctx context.Context, path string) (string, error)
	// Version is wb's stamped build version, "" when unstamped, which
	// its bundle's must equal.
	Version string
	// NoCore sets wb's core file limit to 0 (internal/platform/sessionproc).
	NoCore func() error
	// Unavailable is set when wb's own path can't be found.
	Unavailable error
	Log         *slog.Logger
}

// command is one parsed `wb cred` command: the wb-credset operation and
// the binding, "" for list.
type command struct {
	op      string
	binding string
}

const usageText = `Usage: wb cred set <binding>
       wb cred list
       wb cred rm <binding>

A binding name is 1 to 64 characters of a-z, 0-9 and -.

set reads the secret from a prompt that doesn't show it, or, when
standard input isn't a terminal, from standard input to its end. One
trailing newline is removed, so "echo token | wb cred set github" stores
"token".
`

// Run runs `wb cred <args>` and returns the exit status.
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 {
		return usage(env, "wb cred needs a subcommand")
	}
	sub, rest := args[0], args[1:]
	switch {
	case sub == "help" || sub == "-h" || sub == "--help":
		fmt.Fprint(env.Stdout, usageText)
		return exitOK
	case sub == "list" && len(rest) == 0:
		return runCommand(ctx, env, command{op: credset.OpList})
	case sub == "set" && len(rest) == 1:
		return runCommand(ctx, env, command{op: credset.OpWrite, binding: rest[0]})
	case sub == "rm" && len(rest) == 1:
		return runCommand(ctx, env, command{op: credset.OpDelete, binding: rest[0]})
	case sub == "list" || sub == "set" || sub == "rm":
		return usage(env, "wrong number of arguments for wb cred "+sub)
	}
	return usage(env, fmt.Sprintf("wb cred has no subcommand %q", printable(sub)))
}

func usage(env Env, msg string) int {
	fmt.Fprintf(env.Stderr, "wb: %s\n\n%s", msg, usageText)
	return exitUsage
}

// runCommand runs one command: the host check, the name check, the
// bundle, the value for set, then wb-credset. The name is checked before
// anything is read, so a bad name never makes the user type a secret.
func runCommand(ctx context.Context, env Env, c command) int {
	if platform.HostSupport(env.Host.OS) != platform.V1 || env.Host.WSL {
		return fail(env, RuleHostLater, "wb cred on this host is later in the platform matrix (S12-platforms), and runs on macOS hosts only", "")
	}
	if c.op != credset.OpList {
		if _, ok := credset.BindingItem(c.binding); !ok {
			fail(env, RuleNameInvalid, "the binding name isn't 1 to 64 characters of a-z, 0-9 and -", "")
			return exitUsage
		}
	}
	helper, err := helperPath(env)
	if err != nil {
		return fail(env, RuleNoBundle, err.Error(), noBundleFix)
	}
	var value []byte
	if c.op == credset.OpWrite {
		// No core file of wb can hold the value (S13-01-process-hardening,
		// "Limits and environment"). The helper inherits the limit, and
		// sets it again itself.
		if err := env.NoCore(); err != nil {
			return fail(env, RuleNoCore, "wb can't turn its core files off before it reads the secret: "+err.Error(), "")
		}
		buf, v, reason := readValue(env, c.binding)
		defer clear(buf)
		if reason != "" {
			return fail(env, RuleValueRefused, reason, "")
		}
		value = v
	}
	recordAccess(ctx, env, c, "")
	res := runHelper(ctx, env, helper, c, value)
	recordAccess(ctx, env, c, res.rule())
	return report(ctx, env, c, helper, res)
}

// recordAccess is the hook where the audit record of each `wb cred`
// access goes (S15-03-access-rules, rule 6; SEC10-audit): before
// wb-credset runs, with rule "", and with its outcome after. The record
// holds the operation and the binding name, never the value. wb is to
// ask wb-hostd to write it, and refuse before wb-credset runs when the
// first record can't be written (I514). Until then wb logs the access
// at warning level, which wb's log for `wb cred` shows on standard
// error, and wb-credset logs its decision.
func recordAccess(ctx context.Context, env Env, c command, rule string) {
	phase := "start"
	if rule != "" {
		phase = "end"
	}
	env.Log.LogAttrs(ctx, slog.LevelWarn, "wb cred access",
		slog.String("phase", phase),
		slog.String("op", c.op),
		slog.String("binding", c.binding),
		slog.String("rule", rule))
}

// fail prints fix, when there is one, and then the last line of a
// failure, `wraith box: <message> (<rule>)`, and returns 255.
func fail(env Env, rule, msg, fix string) int {
	if fix != "" {
		fmt.Fprintln(env.Stderr, strings.TrimRight(fix, "\n"))
	}
	fmt.Fprintf(env.Stderr, "wraith box: %s (%s)\n", printable(msg), printable(rule))
	return exitFailure
}

// maxPrinted bounds a text from wb-credset that wb prints.
const maxPrinted = 2048

// printable removes the escaped set of S16-03-around-the-stream from s,
// then cuts it, so text from another process can't rewrite or hide part
// of the terminal (SEC14-no-fake-approvals).
func printable(s string) string {
	s = guesttext.Strip(s)
	if len(s) > maxPrinted {
		s = strings.ToValidUTF8(s[:maxPrinted], "") + " [cut]"
	}
	return s
}
