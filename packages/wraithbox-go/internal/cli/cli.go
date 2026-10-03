// Package cli parses the wb command line: wb [global flags] <command> [args].
// See docs/spec/005-cli.md.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Command describes one wb subcommand.
type Command struct {
	Name    string
	Summary string
	// Agent marks a command that starts an agent session. Everything after
	// an agent command is passed to the agent unchanged, and only agent
	// commands accept the session flags.
	Agent bool
}

// Commands is the wb command table, in help order (spec 005).
var Commands = []Command{
	{Name: "claude", Summary: "Run Claude Code in a sandbox; all further arguments go to claude", Agent: true},
	{Name: "status", Summary: "VMs, running sessions and pending approvals"},
	{Name: "sessions", Summary: "List sessions and their returned work"},
	{Name: "diff", Summary: "Review a session's returned work, with risky paths flagged"},
	{Name: "land", Summary: "Fetch a session's work into the host repository as a branch"},
	{Name: "discard", Summary: "Drop a session's returned work and worktree"},
	{Name: "approve", Summary: "Answer a pending approval with allow"},
	{Name: "deny", Summary: "Answer a pending approval with deny"},
	{Name: "allow", Summary: "Add an allowlist entry"},
	{Name: "policy", Summary: "Show, explain or edit the effective policy"},
	{Name: "learn", Summary: "Suggested allowlist from a learn-mode session"},
	{Name: "trust", Summary: "Allow a repository's own configuration"},
	{Name: "untrust", Summary: "Stop honouring a repository's own configuration"},
	{Name: "cred", Summary: "Manage credentials held on the host"},
	{Name: "audit", Summary: "Read the audit log"},
	{Name: "vm", Summary: "Start, stop, suspend and inspect VMs"},
	{Name: "image", Summary: "Build, list and select guest images"},
	{Name: "shell", Summary: "Debug shell as a project's guest user"},
	{Name: "setup", Summary: "Check prerequisites and install the host services"},
	{Name: "version", Summary: "Print the version"},
	{Name: "help", Summary: "Show help"},
}

// Globals holds the flags given before the command. Isolated, Ephemeral,
// Learn and Guest are session flags, valid only before an agent command.
// Guest is checked against the platform matrix when the session starts,
// not here.
type Globals struct {
	Isolated  bool   // run in the isolated VM (S8)
	Ephemeral bool   // no persisted state (F14)
	Learn     bool   // learn mode; refused later unless the project is trusted (F10)
	Guest     string // guest OS for this project (spec 012); empty means the default
	Dir       string // act as if started in this directory
	Help      bool
	Version   bool
}

// Invocation is a parsed wb command line.
type Invocation struct {
	Globals Globals
	// Command is nil when no command was given.
	Command *Command
	// Args are the arguments after the command, unchanged.
	Args []string
}

// ErrUsage wraps every command-line error, so callers can map it to exit code 2.
var ErrUsage = errors.New("usage error")

// Parse parses args (without the program name). Flags are only recognised
// before the command; the first non-flag argument is the command and every
// argument after it belongs to that command, untouched.
func Parse(args []string) (Invocation, error) {
	var inv Invocation
	fs := flag.NewFlagSet("wb", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&inv.Globals.Isolated, "isolated", false, "")
	fs.BoolVar(&inv.Globals.Ephemeral, "ephemeral", false, "")
	fs.BoolVar(&inv.Globals.Learn, "learn", false, "")
	fs.StringVar(&inv.Globals.Guest, "guest", "", "")
	fs.StringVar(&inv.Globals.Dir, "C", "", "")
	fs.BoolVar(&inv.Globals.Help, "help", false, "")
	fs.BoolVar(&inv.Globals.Help, "h", false, "")
	fs.BoolVar(&inv.Globals.Version, "version", false, "")
	if err := fs.Parse(args); err != nil {
		return Invocation{}, fmt.Errorf("%w: %w", ErrUsage, err)
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return inv, nil
	}
	i := slices.IndexFunc(Commands, func(c Command) bool { return c.Name == rest[0] })
	if i < 0 {
		return Invocation{}, fmt.Errorf("%w: unknown command %q", ErrUsage, rest[0])
	}
	inv.Command = &Commands[i]
	inv.Args = rest[1:]

	g := inv.Globals
	if !inv.Command.Agent && (g.Isolated || g.Ephemeral || g.Learn || g.Guest != "") {
		return Invocation{}, fmt.Errorf("%w: --isolated, --ephemeral, --learn and --guest only apply to agent commands such as claude", ErrUsage)
	}
	return inv, nil
}

// Help returns the top-level help text.
func Help() string {
	var b strings.Builder
	b.WriteString("Usage: wb [flags] <command> [args]\n\n")
	b.WriteString("Flags (before the command):\n")
	b.WriteString("  --isolated    run the session in the isolated VM\n")
	b.WriteString("  --ephemeral   keep no state after the session\n")
	b.WriteString("  --learn       learn mode, trusted projects only\n")
	b.WriteString("  --guest <os>  guest OS for the project: macos, linux or windows\n")
	b.WriteString("  -C <dir>      run as if started in <dir>\n\n")
	b.WriteString("Commands:\n")
	for _, c := range Commands {
		fmt.Fprintf(&b, "  %-10s %s\n", c.Name, c.Summary)
	}
	b.WriteString("\nExample: wb --isolated claude --resume\n")
	return b.String()
}
