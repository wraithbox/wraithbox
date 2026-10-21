package relay

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/termfilter"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// received is one result of Stream.Recv.
type received struct {
	frame *terminalv1.ServerFrame
	err   error
}

// chunk is one read of standard input: bytes, or the error that ended
// it.
type chunk struct {
	data []byte
	err  error
}

// output is one of wb's standard output and standard error, with its
// own filter (S16-01-filter, "Where").
type output struct {
	name   string
	w      *lastByte
	filter *termfilter.Filter
	tty    bool
	// used is set once an output frame came for it.
	used bool
	// closed is set after a write failed: later output is discarded.
	closed bool
}

func newOutput(name string, w io.Writer, tty bool) *output {
	lb := &lastByte{w: w}
	return &output{name: name, w: lb, filter: termfilter.New(lb), tty: tty}
}

// lastByte is a writer that remembers the last byte written through it,
// so the report on standard error can start a line of its own.
type lastByte struct {
	w     io.Writer
	last  byte
	wrote bool
}

func (l *lastByte) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	if n > 0 {
		l.last, l.wrote = p[n-1], true
	}
	return n, err
}

// unended reports whether the last byte written didn't end a line.
func (l *lastByte) unended() bool { return l.wrote && l.last != '\n' }

// relay is the state of one Run. Only the goroutine that runs Run
// reads and writes it, except for the channels.
type relay struct {
	cfg         Config
	log         *slog.Logger
	interactive bool
	out, errOut *output

	frames chan received
	input  chan chunk
	panics chan any
	done   chan struct{}

	// restoreRaw restores the terminal mode while raw mode is on, and is
	// nil otherwise.
	restoreRaw func() error
	// titled is set while wb's title is on the terminal.
	titled bool
	// hungUp is set after SIGHUP: nothing more goes to a terminal.
	hungUp bool
	// selfStopped is set from wb's stop for job control to its resume.
	selfStopped bool
	// stopTimer runs from a stop frame to the stopped frame.
	stopTimer *time.Timer
	stopC     <-chan time.Time
	// broken is the error of a failed Send. Nothing more is sent, and the
	// relay reads on until the stream's last frame or its error, which
	// decides the exit status: a call that wb-hostd ended fails a Send
	// before wb reads the exit frame queued behind it.
	broken error
	// started is set once the goroutines that read the stream and
	// standard input run.
	started bool
	// lateStopped is set when wb stopped itself without the stopped
	// frame it asked for. The one that arrives late is ignored, so wb
	// doesn't stop twice. Output from the guest clears it.
	lateStopped bool
	// writeFailed is the error of a failed output write other than
	// EPIPE on standard output in non-interactive use.
	writeFailed error
}

func newRelay(cfg Config) *relay {
	return &relay{
		cfg:         cfg,
		log:         cfg.Log,
		interactive: cfg.TTY.Interactive(),
		out:         newOutput("standard output", cfg.Stdout, cfg.TTY.Stdout),
		errOut:      newOutput("standard error", cfg.Stderr, cfg.TTY.Stderr),
		frames:      make(chan received),
		input:       make(chan chunk),
		panics:      make(chan any, 2),
		done:        make(chan struct{}),
	}
}

// start sets raw mode and the title, and starts reading the stream and
// standard input.
func (r *relay) start() *failure {
	if r.interactive {
		if r.cfg.Terminal == nil {
			return &failure{rule: RuleTerminal, message: "standard input is a terminal that can't be set up"}
		}
		if err := r.makeRaw(); err != nil {
			return &failure{rule: RuleTerminal, message: "the terminal can't be set to raw mode: " + err.Error()}
		}
	}
	r.setTitle()
	r.goSafe(r.receive)
	r.goSafe(r.read)
	r.started = true
	return nil
}

// goSafe runs fn in a goroutine. A panic there ends the relay through
// the panics channel, so Run still restores the terminal.
func (r *relay) goSafe(fn func()) {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				r.log.Error("relay: panic", "panic", p, "stack", string(debug.Stack()), "rule", RulePanic)
				select {
				case r.panics <- p:
				default:
				}
			}
		}()
		fn()
	}()
}

// receive passes each frame of the stream to the loop, until an error
// or the last frame.
func (r *relay) receive() {
	for {
		f, err := r.cfg.Stream.Recv()
		select {
		case r.frames <- received{f, err}:
		case <-r.done:
			return
		}
		if err != nil || f.GetExit() != nil || f.GetFailure() != nil {
			return
		}
	}
}

// read passes standard input to the loop in chunks of at most
// MaxFrameData bytes, until an error, io.EOF included.
func (r *relay) read() {
	for {
		buf := make([]byte, MaxFrameData)
		n, err := r.cfg.Stdin.Read(buf)
		if n > 0 && !r.pass(chunk{data: buf[:n]}) {
			return
		}
		if err != nil {
			r.pass(chunk{err: err})
			return
		}
	}
}

func (r *relay) pass(c chunk) bool {
	select {
	case r.input <- c:
		return true
	case <-r.done:
		return false
	}
}

// loop handles frames, input and signals until the stream ends.
func (r *relay) loop() end {
	for {
		select {
		case rc := <-r.frames:
			if e, done := r.onFrame(rc); done {
				return e
			}
		case c := <-r.input:
			r.onInput(c)
		case s := <-r.cfg.Signals:
			r.onSignal(s)
		case <-r.stopC:
			r.log.Info("relay: no stopped frame in time, stopping anyway", "wait", r.cfg.StopWait)
			r.stop()
			r.lateStopped = true
		case p := <-r.panics:
			return end{code: ExitFailure, fail: &failure{rule: RulePanic, message: fmt.Sprintf("internal error in the terminal relay: %v", p)}}
		}
	}
}

func (r *relay) onFrame(rc received) (end, bool) {
	if rc.err != nil {
		return streamEnd(rc.err), true
	}
	switch f := rc.frame.GetFrame().(type) {
	case *terminalv1.ServerFrame_Output:
		if fail := r.onOutput(f.Output); fail != nil {
			return end{code: ExitFailure, fail: fail}, true
		}
	case *terminalv1.ServerFrame_Stopped:
		r.onStopped()
	case *terminalv1.ServerFrame_Exit:
		return exitEnd(f.Exit), true
	case *terminalv1.ServerFrame_Failure:
		return failureEnd(f.Failure), true
	default:
		return frameEnd("a server frame with nothing set"), true
	}
	return end{}, false
}

// onOutput checks an output frame and writes it through its stream's
// filter. Nothing reaches standard output or standard error but
// through a filter (S16-01-filter, "Where").
func (r *relay) onOutput(o *terminalv1.Output) *failure {
	if len(o.GetData()) > MaxFrameData {
		return frameEnd(fmt.Sprintf("an output frame of %d bytes, over %d", len(o.GetData()), MaxFrameData)).fail
	}
	dst, fail := r.outputFor(o.GetStream())
	if fail != nil {
		return fail
	}
	r.lateStopped = false
	r.write(dst, o.GetData())
	return nil
}

// outputFor returns the output of a stream. A standard error frame is
// out of bounds when standard error has no frames of its own.
func (r *relay) outputFor(s terminalv1.Stream) (*output, *failure) {
	if s == terminalv1.Stream_STREAM_STDOUT {
		return r.out, nil
	}
	if s != terminalv1.Stream_STREAM_STDERR {
		return nil, frameEnd(fmt.Sprintf("an output frame for stream %d", s)).fail
	}
	if !r.cfg.TTY.SeparateStderr() {
		return nil, frameEnd("a standard error frame, which this setup has none of").fail
	}
	return r.errOut, nil
}

func (r *relay) write(o *output, data []byte) {
	o.used = true
	if o.closed || (r.hungUp && o.tty) {
		return
	}
	if _, err := o.filter.Write(data); err != nil {
		r.writeError(o, err)
	}
}

// writeError handles a failed write: EPIPE on standard output in
// non-interactive use tells the guest to close the program's standard
// output, and wb waits for the exit (S05-02-exit-status-and-signals, "A
// closed output"). Any other failure ends wb with 255 once the program
// exits.
func (r *relay) writeError(o *output, err error) {
	o.closed = true
	if o == r.out && !r.interactive && errors.Is(err, syscall.EPIPE) {
		r.log.Info("relay: standard output closed by its reader", "rule", RuleStdoutClosed)
		r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_StdoutClosed{StdoutClosed: &terminalv1.StdoutClosed{}}})
		return
	}
	if errors.Is(err, syscall.EPIPE) {
		r.log.Info("relay: output closed by its reader", "output", o.name)
		return
	}
	r.log.Error("relay: output write failed", "output", o.name, "err", err, "rule", RuleOutputWrite)
	if r.writeFailed == nil {
		r.writeFailed = fmt.Errorf("writing %s failed: %w", o.name, err)
	}
	if o == r.out && !r.interactive {
		r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_StdoutClosed{StdoutClosed: &terminalv1.StdoutClosed{}}})
	}
}

// onInput forwards input to the guest, unfiltered
// (S16-03-around-the-stream, "Input drain"). The end of standard input
// in non-interactive use becomes a standard-input-closed frame. After
// SIGHUP, the terminal's input is gone, but a pipe's still goes through.
func (r *relay) onInput(c chunk) {
	if c.err != nil {
		r.input = nil
		if r.interactive {
			r.log.Info("relay: terminal input ended", "err", c.err)
			return
		}
		r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_StdinClosed{StdinClosed: &terminalv1.StdinClosed{}}})
		return
	}
	if r.hungUp && r.interactive {
		return
	}
	r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Input{Input: c.data}})
}

func (r *relay) send(f *terminalv1.ClientFrame) {
	if r.broken != nil {
		return
	}
	if err := r.cfg.Stream.Send(f); err != nil {
		r.log.Info("relay: send failed, reading on to the last frame", "err", err)
		r.broken = err
	}
}

// onSignal handles a signal as S05-02-exit-status-and-signals says.
func (r *relay) onSignal(s tty.Signal) {
	r.log.Info("relay: signal", "signal", s.String())
	switch s {
	case tty.WindowChange:
		r.resize()
	case tty.Interrupt:
		r.sendSignal(terminalv1.Signal_SIGNAL_INT)
	case tty.Quit:
		r.sendSignal(terminalv1.Signal_SIGNAL_QUIT)
	case tty.Terminate:
		r.sendSignal(terminalv1.Signal_SIGNAL_TERM)
	case tty.Hangup:
		r.hangup()
	case tty.TerminalStop:
		r.terminalStop()
	case tty.Continue:
		r.cont()
	}
}

func (r *relay) sendSignal(s terminalv1.Signal) {
	r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Signal{Signal: s}})
}

// resize sends the window size, in interactive use.
func (r *relay) resize() {
	if !r.interactive || r.hungUp {
		return
	}
	size, ok := r.size()
	if !ok {
		return
	}
	r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Resize{Resize: size}})
}

func (r *relay) size() (*terminalv1.WindowSize, bool) {
	rows, cols, err := r.cfg.Terminal.Size()
	if err != nil || rows < 1 || rows > maxWindow || cols < 1 || cols > maxWindow {
		r.log.Info("relay: window size left out", "rows", rows, "cols", cols, "err", err, "rule", RuleWindowSize)
		return nil, false
	}
	return &terminalv1.WindowSize{Rows: uint32(rows), Cols: uint32(cols)}, true
}

// hangup sends the hangup frame. wb then writes nothing more to a
// terminal and waits for the last frame (S05-02-exit-status-and-signals,
// "Hangup").
func (r *relay) hangup() {
	if r.hungUp {
		return
	}
	r.hungUp = true
	r.cancelStopTimer()
	r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}})
}

// terminalStop handles a SIGTSTP from outside. In interactive use wb
// asks the guest to stop the program and waits at most StopWait for the
// stopped frame. In non-interactive use it forwards the signal and stops
// itself (S05-02-exit-status-and-signals, "Job control").
func (r *relay) terminalStop() {
	if r.hungUp {
		return
	}
	if !r.interactive {
		r.sendSignal(terminalv1.Signal_SIGNAL_TSTP)
		r.giveBack()
		r.stopSelf()
		return
	}
	if r.stopTimer != nil || r.selfStopped {
		return
	}
	r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Stop{Stop: &terminalv1.Stop{}}})
	r.stopTimer = time.NewTimer(r.cfg.StopWait)
	r.stopC = r.stopTimer.C
}

func (r *relay) cancelStopTimer() {
	if r.stopTimer != nil {
		r.stopTimer.Stop()
	}
	r.stopTimer, r.stopC = nil, nil
}

// onStopped handles a stopped frame. It is guest input, and can only
// make wb give the terminal back early. In non-interactive use there is
// no terminal to give back, and it is ignored.
func (r *relay) onStopped() {
	if !r.interactive {
		r.log.Info("relay: stopped frame ignored in non-interactive use", "rule", RuleStoppedIgnored)
		return
	}
	if r.lateStopped {
		r.lateStopped = false
		r.log.Info("relay: late stopped frame ignored, wb already stopped for it", "rule", RuleStoppedIgnored)
		return
	}
	r.stop()
}

// giveBack writes what wb writes before it stops itself: the stop
// reset to each output that is a terminal, the drain, and the user's
// title (S16-03-around-the-stream, "Around the stream").
func (r *relay) giveBack() {
	r.writeReset(r.out, true)
	r.writeReset(r.errOut, true)
	r.drain()
	r.restoreTitle()
}

// stop gives the terminal back and stops wb, in interactive use: the
// reset and the drain, the user's title, cooked mode, then SIGSTOP. On
// SIGCONT, cont sets it all up again.
func (r *relay) stop() {
	r.cancelStopTimer()
	if r.hungUp || r.selfStopped {
		return
	}
	r.giveBack()
	r.restoreMode()
	r.selfStopped = true
	if err := r.cfg.StopSelf(); err != nil {
		r.log.Error("relay: wb can't stop itself", "err", err)
		r.resume()
	}
}

func (r *relay) stopSelf() {
	if err := r.cfg.StopSelf(); err != nil {
		r.log.Error("relay: wb can't stop itself", "err", err)
	}
}

// cont handles SIGCONT. After wb's own stop in interactive use, it
// resumes. In non-interactive use it forwards the signal.
func (r *relay) cont() {
	if r.hungUp {
		return
	}
	if !r.interactive {
		r.setTitle()
		r.sendSignal(terminalv1.Signal_SIGNAL_CONT)
		return
	}
	if r.selfStopped {
		r.resume()
	}
}

// resume sets raw mode and the title again, sends the window size,
// which may have changed, and has the guest continue the program.
func (r *relay) resume() {
	r.selfStopped = false
	if err := r.makeRaw(); err != nil {
		r.log.Error("relay: raw mode after a stop failed", "err", err, "rule", RuleTerminal)
	}
	r.setTitle()
	r.resize()
	r.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Resume{Resume: &terminalv1.Resume{}}})
}
