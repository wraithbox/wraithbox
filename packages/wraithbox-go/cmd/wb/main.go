// Command wb is the single Wraith Box entry point: wb [flags] <command> [args].
// `wb claude [claude args]` runs Claude Code in a sandbox. See docs/spec/S05-cli/S05-00-cli.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/cli"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectcmd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/setup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmcmd"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	inv, err := cli.Parse(args)
	if errors.Is(err, cli.ErrUsage) {
		fmt.Fprintf(os.Stderr, "wb: %v\n\n%s", err, cli.Help())
		return 2
	}
	switch {
	case inv.Globals.Version || (inv.Command != nil && inv.Command.Name == "version"):
		fmt.Println(version.String("wb"))
		return 0
	case inv.Globals.Help || inv.Command == nil || inv.Command.Name == "help":
		fmt.Print(cli.Help())
		return 0
	case inv.Command.Name == "setup":
		// Each refusal and its fix go to standard error from setup itself,
		// so the log shows only errors here.
		log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		return setup.Run(context.Background(), setup.SystemEnv(log), inv.Args)
	case inv.Command.Name == "vm":
		log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		return vmcmd.Run(context.Background(), vmcmd.SystemEnv(log), inv.Args)
	case inv.Command.Name == "project" || inv.Command.Name == "shell":
		return runInProject(inv)
	}
	fmt.Fprintf(os.Stderr, "wb %s: not implemented yet\n", inv.Command.Name)
	return 2
}

// runInProject runs a command that resolves its project from the
// directory of -C, or the current one: `wb project` and `wb shell`.
func runInProject(inv cli.Invocation) int {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dir := inv.Globals.Dir
	if dir == "" {
		dir = "."
	}
	if inv.Command.Name == "shell" {
		return projectcmd.RunShell(context.Background(), projectcmd.SystemEnv(log), dir, inv.Args)
	}
	return projectcmd.Run(context.Background(), projectcmd.SystemEnv(log), dir, inv.Args)
}
