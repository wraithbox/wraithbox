package credcmd

import (
	"log/slog"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
)

// exitInterrupted is the exit status after Ctrl-C at the prompt: 128
// plus SIGINT's 2, as a shell reports it.
const exitInterrupted = 130

// SystemEnv returns the Env of wb on this host.
func SystemEnv(log *slog.Logger) Env {
	fd := int(os.Stdin.Fd())
	env := Env{
		Host:          platform.Detect(),
		Stdin:         os.Stdin,
		StdinTerminal: tty.IsTerminal(fd),
		ReadHidden:    func() ([]byte, error) { return readHidden(fd) },
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		LookupEnv:     os.LookupEnv,
		ReadTeam:      codesign.ReadTeam,
		Log:           log,
	}
	env.Executable, env.Unavailable = os.Executable()
	return env
}

// readHidden reads one line from the terminal on fd with echo off. On
// Ctrl-C it turns echo back on before wb exits, which term.ReadPassword
// doesn't do by itself.
func readHidden(fd int) ([]byte, error) {
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	done := make(chan struct{})
	defer func() {
		signal.Stop(interrupted)
		close(done)
	}()
	go func() {
		select {
		case <-interrupted:
			_ = term.Restore(fd, state)        // wb exits next, and the terminal is all that is left to restore.
			_, _ = os.Stderr.WriteString("\n") // the line end after the prompt, and wb exits next.
			os.Exit(exitInterrupted)
		case <-done:
		}
	}()
	return term.ReadPassword(fd)
}
