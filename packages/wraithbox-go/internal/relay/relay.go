// Package relay is wb's end of the terminal stream of
// wraithbox.terminal.v1: it relays the user's terminal to a program in
// the guest, and the program's output back through the terminal filter.
// `wb shell` uses it, and `wb claude` will (S05-01-agent-commands,
// S05-02-exit-status-and-signals, S16-terminal-stream).
//
// The guest's output reaches the host terminal here, boundary 6 of
// T00-index. Every server frame is guest input, also after wb-hostd
// relayed it (SEC11-root-gains-nothing):
//
//   - every output frame passes a termfilter.Filter, one for standard
//     output and one for standard error, whether they go to a terminal,
//     a pipe or a file (S16-01-filter, "Where");
//   - a frame out of the bounds of wraithbox.terminal.v1 ends the relay
//     with exit status 255 (frames.go);
//   - wb writes only its own title, reset and drain around the stream,
//     and only to a terminal (S16-03-around-the-stream), so a guest
//     can't fake a summary or an approval prompt after the session
//     (SEC03-no-host-exec, SEC14-no-fake-approvals).
//
// The relay takes the stream, the terminal and the signals as
// interfaces, so tests drive it with fakes. What they are on an OS is in
// internal/platform/tty, and System wires them.
package relay

import (
	"io"
	"log/slog"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// Exit statuses of S05-02-exit-status-and-signals, "Exit status". The
// program's own code n gives n, and its signal n gives 128 + n.
const (
	// ExitUsage is a usage error in the wb flags.
	ExitUsage = 2
	// ExitFailure is any failure of Wraith Box. The last line on
	// standard error then starts with `wraith box:`.
	ExitFailure = 255
)

// Times of S16-03-around-the-stream and S05-02-exit-status-and-signals.
const (
	// drainTimeout is how long the input drain waits for the reply to
	// its sentinel query (S16-03-around-the-stream, "Input drain").
	drainTimeout = 100 * time.Millisecond
	// stopWait is how long wb waits for a stopped frame after it asked
	// the guest to stop the program, on a SIGTSTP from outside, before
	// it gives the terminal back anyway (S05-02-exit-status-and-signals,
	// "Job control").
	stopWait = time.Second
)

// Stream is wb's end of a terminal stream: the frames after the first
// message of a Shell call. Send and Recv may run at the same time, as
// on a gRPC stream. Recv returns io.EOF when the stream closed.
type Stream interface {
	Send(*terminalv1.ClientFrame) error
	Recv() (*terminalv1.ServerFrame, error)
	CloseSend() error
}

// Terminal is the terminal on wb's standard input
// (internal/platform/tty.Terminal).
type Terminal interface {
	// MakeRaw sets raw mode without ISIG, and returns a function that
	// restores the mode before it.
	MakeRaw() (restore func() error, err error)
	// Size returns the window size.
	Size() (rows, cols int, err error)
}

// TTYs says which of wb's standard streams are terminals.
type TTYs struct {
	Stdin, Stdout, Stderr bool
}

// Interactive reports interactive use: standard input and standard
// output are both terminals (S05-01-agent-commands, "Interactive and
// non-interactive use").
func (t TTYs) Interactive() bool { return t.Stdin && t.Stdout }

// SeparateStderr reports whether standard error has output frames of
// its own: always in non-interactive use, and in interactive use when
// standard error isn't a terminal.
func (t TTYs) SeparateStderr() bool { return !t.Interactive() || !t.Stderr }

// Config is what one relay runs with.
type Config struct {
	Stream Stream
	// Terminal is the terminal on standard input. Interactive use needs
	// it.
	Terminal Terminal
	// Signals delivers the signals of S05-02-exit-status-and-signals.
	Signals <-chan tty.Signal
	// StopSelf stops wb until it continues (tty.StopSelf).
	StopSelf func() error
	// Charset is the host terminal's charset (tty.HostCharset). Run
	// refuses one that isn't UTF-8, the zero Charset included.
	Charset tty.Charset

	Stdin          io.Reader
	Stdout, Stderr io.Writer
	TTY            TTYs

	// ID names the program in the window title, `wb: <ID>`: the shell
	// or session id. The relay strips it again before it writes it.
	ID string

	Log *slog.Logger

	// DrainTimeout and StopWait replace drainTimeout and stopWait when
	// set, for tests.
	DrainTimeout, StopWait time.Duration
}

func (c *Config) defaults() {
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
	if c.DrainTimeout == 0 {
		c.DrainTimeout = drainTimeout
	}
	if c.StopWait == 0 {
		c.StopWait = stopWait
	}
	if c.StopSelf == nil {
		c.StopSelf = func() error { return nil }
	}
}

// Run refuses a host terminal whose charset isn't UTF-8 before it reads
// a frame (CheckCharset). Otherwise it relays until the stream's last
// frame, or until it fails, and
// returns wb's exit status by the table of
// S05-02-exit-status-and-signals. On every way out, a panic included, it
// restores the terminal mode it found. Unless wb got SIGHUP, it then
// writes the reset, drains the input, restores the title, and prints
// the drop summary and a `wraith box:` line for a failure on standard
// error.
//
// Run leaves the goroutine that reads standard input running when it
// returns: a read from a terminal can't be cancelled, and `wb shell`
// exits right after. A caller that goes on after Run, such as `wb
// claude` with its summary of returned work, must not read standard
// input again.
func Run(cfg Config) (code int) {
	cfg.defaults()
	if CheckCharset(cfg.Charset, cfg.Log) != nil {
		return refuse(cfg, (&CharsetError{Charset: cfg.Charset}).Line())
	}
	r := newRelay(cfg)
	defer r.recoverPanic(&code)
	if f := r.start(); f != nil {
		return r.finish(end{code: ExitFailure, fail: f})
	}
	return r.finish(r.loop())
}
