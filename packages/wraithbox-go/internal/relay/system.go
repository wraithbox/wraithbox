package relay

import (
	"fmt"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
)

// System returns a Config with the parts that come from the OS: the
// standard streams, which of them are terminals, the terminal on
// standard input, the signals, and the stop for job control. The caller
// sets the stream, the id and the log. stop ends the signal delivery.
// On an OS where internal/platform/tty isn't built, it fails with an
// error that wraps errors.ErrUnsupported.
func System() (cfg Config, stop func(), err error) {
	sigs := make(chan tty.Signal, 8)
	stop, err = tty.Notify(sigs)
	if err != nil {
		return Config{}, nil, fmt.Errorf("relay: %w", err)
	}
	cfg = Config{
		Signals:  sigs,
		StopSelf: tty.StopSelf,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		TTY: TTYs{
			Stdin:  tty.IsTerminal(int(os.Stdin.Fd())),
			Stdout: tty.IsTerminal(int(os.Stdout.Fd())),
			Stderr: tty.IsTerminal(int(os.Stderr.Fd())),
		},
	}
	if cfg.TTY.Stdin {
		t, err := tty.NewTerminal(int(os.Stdin.Fd()))
		if err != nil {
			stop()
			return Config{}, nil, fmt.Errorf("relay: %w", err)
		}
		cfg.Terminal = t
	}
	return cfg, stop, nil
}
