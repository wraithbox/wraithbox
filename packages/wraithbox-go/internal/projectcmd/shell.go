package projectcmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/grpc"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/relay"
)

// RuleTerminalUnsupported: wb can't use the terminal or the host's
// standard streams for a shell, such as on a host whose terminal
// support comes later (S12-platforms).
const RuleTerminalUnsupported = "terminal-unsupported"

// RuleOpenSignal: wb got SIGINT, SIGQUIT, SIGTERM or SIGHUP while it
// waited for the shell to open, and stopped waiting.
const RuleOpenSignal = "shell-open-signal"

// ShellHostd is wb-hostd's ProjectService and ShellService on one
// connection.
type ShellHostd interface {
	Hostd
	hostdv1.ShellServiceClient
}

const shellUsageText = `Usage: wb shell [--project P]

P is a project id or name. Without --project, the project is the one of
the repository in the current directory, or in the directory of -C.
`

// parseShell reads `--project P` or `--project=P`. Any other argument is
// a usage error: wb shell takes no command to run
// (S05-03-management-commands, "`wb shell`").
func parseShell(args []string) (invocation, error) {
	inv := invocation{sub: "shell"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var err error
		switch {
		case a == "--project":
			if i+1 == len(args) {
				return invocation{}, errors.New("--project needs a project id or name")
			}
			i++
			err = inv.setProject(args[i])
		case strings.HasPrefix(a, "--project="):
			err = inv.setProject(strings.TrimPrefix(a, "--project="))
		default:
			err = fmt.Errorf("wb shell takes no argument but --project, and got %q", a)
		}
		if err != nil {
			return invocation{}, err
		}
	}
	return inv, nil
}

// RunShell runs `wb shell <args>` in dir, the directory of `-C` or the
// current one, and returns the exit status: a usage error 2, a refusal
// 255, and otherwise the shell's by S05-02-exit-status-and-signals. It
// resolves the project as `wb project` does, opens a debug shell in its
// VM, prints the label line, and relays the shell through the terminal
// filter (internal/relay).
func RunShell(ctx context.Context, env Env, dir string, args []string) int {
	inv, err := parseShell(args)
	if err != nil {
		fmt.Fprintf(env.Stderr, "wb: %s\n\n%s", printable(err.Error()), shellUsageText)
		return exitUsage
	}
	if env.Unavailable != nil {
		return fail(env, RuleUnavailable, "wb can't find its paths: "+env.Unavailable.Error())
	}
	cfg, stop, err := env.Terminal()
	if err != nil {
		return fail(env, RuleTerminalUnsupported, "wb shell can't use this host's terminal: "+err.Error())
	}
	defer stop()
	setup, code := shellSetup(env, cfg)
	if setup == nil {
		return code
	}
	hostd, err := env.DialShell(ctx)
	if err != nil {
		return fail(env, RuleUnreachable, "wb-hostd can't be reached: "+err.Error())
	}
	defer func() { _ = hostd.Close() }()
	c := &cmd{
		env: env, dir: dir, inv: inv, hostd: hostd,
		open: &project.Opener{Paths: env.Paths, Hostd: hostd, Log: env.Log},
	}
	// The stream runs on streamCtx, which a signal during the open
	// cancels.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stream relay.Stream
	var sh relay.Shell
	sig, err := watchOpen(cancel, cfg.Signals, env.Log, func() error {
		var err error
		stream, sh, err = c.openShell(streamCtx, hostd, setup)
		return err
	})
	if sig != 0 {
		return fail(env, RuleOpenSignal, fmt.Sprintf("wb shell got %s while the shell opened, and stopped.", sig))
	}
	if err != nil {
		return c.report(err)
	}
	fmt.Fprintln(env.Stderr, sh.Label())
	cfg.Stream, cfg.ID, cfg.Log = stream, sh.ID, env.Log
	return relay.Run(cfg)
}

// watchOpen runs open, and calls cancel when wb gets SIGINT, SIGQUIT,
// SIGTERM or SIGHUP before open returns. The open can wait minutes for
// a VM to start, and such a signal then ends wb at once instead of
// reaching a shell that opens later. It returns the signal, or 0 and
// open's error. Other signals during the open are logged and dropped:
// the setup already holds the window size, and no shell runs to stop.
func watchOpen(cancel context.CancelFunc, sigs <-chan tty.Signal, log *slog.Logger, open func() error) (tty.Signal, error) {
	done := make(chan error, 1)
	go func() { done <- open() }()
	for {
		select {
		case err := <-done:
			return 0, err
		case s := <-sigs:
			switch s {
			case tty.Interrupt, tty.Quit, tty.Terminate, tty.Hangup:
				cancel()
				<-done
				return s, nil
			}
			log.Info("wb shell: a signal during the open dropped", "signal", s.String())
		}
	}
}

// shellSetup builds the terminal setup of the open. It refuses a host
// terminal whose charset isn't UTF-8 before wb-hostd is asked
// (S05-01-agent-commands, "A terminal that isn't UTF-8"), and prints the
// variables left out of the guest's environment. It returns nil and the
// exit status on a refusal.
func shellSetup(env Env, cfg relay.Config) (*terminalv1.Setup, int) {
	setup, lines, err := relay.NewSetup(cfg.TTY, cfg.Terminal, cfg.Charset, env.Lookup, env.Log)
	if cs, ok := errors.AsType[*relay.CharsetError](err); ok {
		fmt.Fprintln(env.Stderr, cs.Line())
		return nil, exitFailure
	}
	if err != nil {
		return nil, fail(env, RuleTerminalUnsupported, err.Error())
	}
	for _, l := range lines {
		fmt.Fprintln(env.Stderr, l)
	}
	return setup, exitOK
}

// openShell resolves the project and opens its shell, with the receive
// cap of relay.ShellClient on the call (SEC13-bounded-resources).
func (c *cmd) openShell(ctx context.Context, hostd ShellHostd, setup *terminalv1.Setup) (relay.Stream, relay.Shell, error) {
	p, err := c.target(ctx)
	if err != nil {
		return nil, relay.Shell{}, err
	}
	call, err := hostd.Shell(ctx, grpc.MaxCallRecvMsgSize(relay.MaxRecvMsgSize))
	if err != nil {
		return nil, relay.Shell{}, err
	}
	return relay.OpenShell(call, p.GetId(), setup)
}
