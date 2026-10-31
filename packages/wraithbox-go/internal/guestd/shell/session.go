package shell

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// The bounds of a shell's output (S06-09-session-lifecycle, "Session
// helper").
const (
	// chunkSize is the most bytes of one output frame.
	chunkSize = 32 << 10
	// drainWait and drainBytes bound what wb-guestd still sends after
	// the exit report, before the exit frame.
	drainWait  = 100 * time.Millisecond
	drainBytes = 1 << 20
	// inputQueue is how many input frames wait for the program to read
	// them. Past it wb-guestd reads no more frames until it has room.
	inputQueue = 64
	// commandWait bounds the write of one command to the helper.
	commandWait = time.Second
)

// chunk is output from the program. data is nil at the end of its
// stream.
type chunk struct {
	stream terminalv1.Stream
	data   []byte
}

// reportEvent is one report, or the error that ended the reports.
type reportEvent struct {
	r   Report
	err error
}

// loop is a running shell. Only the goroutine of run sends frames and
// changes its fields.
type loop struct {
	s   *session
	ctx context.Context
	// stream is nil once the stream ended. Frames for it are then
	// dropped.
	stream Stream
	done   chan struct{}

	out     chan chunk
	reps    chan reportEvent
	frames  chan *terminalv1.ClientFrame
	recvEnd chan error
	input   chan *terminalv1.ClientFrame

	// outputs counts the output streams that haven't ended.
	outputs int
	hungUp  bool
	killAt  <-chan time.Time
}

// run relays the shell until it has ended.
func (s *session) run(stream Stream) {
	l := &loop{
		s: s, ctx: stream.Context(), stream: stream, done: make(chan struct{}),
		out: make(chan chunk), reps: make(chan reportEvent), frames: make(chan *terminalv1.ClientFrame),
		recvEnd: make(chan error, 1), input: make(chan *terminalv1.ClientFrame, inputQueue),
	}
	go l.readReports()
	go l.readOutput(s.stdout, terminalv1.Stream_STREAM_STDOUT)
	l.outputs = 1
	if s.stderr != nil {
		go l.readOutput(s.stderr, terminalv1.Stream_STREAM_STDERR)
		l.outputs++
	}
	go l.recv(stream, l.frames, l.recvEnd)
	go l.writeInput()
	done := false
	for !done {
		done = l.step()
	}
	close(l.done)
	s.closeEnds()
	s.logOutside(context.Background())
	s.srv.release(s)
	s.log.LogAttrs(context.Background(), slog.LevelInfo, "shell closed")
}

// step handles one event, and reports whether the shell has ended.
func (l *loop) step() bool {
	select {
	case f := <-l.frames:
		l.frame(f)
	case err := <-l.recvEnd:
		l.streamEnded(err)
	case c := <-l.out:
		l.output(c, chunkSize)
	case ev := <-l.reps:
		return l.report(ev)
	case <-l.killAt:
		if !l.s.kill() {
			l.killIncomplete()
			return true
		}
		l.send(&terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{
			Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Signal{Signal: sigKill}},
		}})
		return true
	}
	return false
}

// killIncomplete sends the failure shell-hangup-kill in place of an
// exit frame, when processes of the session may be left after the kill
// (S13-01-process-hardening, "When a session's client goes", step 2).
func (l *loop) killIncomplete() {
	l.send(&terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Failure{
		Failure: &terminalv1.Failure{Rule: RuleKilled, Message: "the shell's processes may still run"},
	}})
}

// sigKill is the signal number of SIGKILL, which every POSIX system
// shares.
const sigKill = 9

func (l *loop) send(f *terminalv1.ServerFrame) {
	if l.stream == nil {
		return
	}
	if err := l.stream.Send(&guestv1.ShellResponse{Frame: f}); err != nil {
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell stream send failed", slog.String("error", err.Error()))
		l.stream = nil
	}
}

// output sends a chunk, at most most bytes of it, and counts the end of
// a stream. It returns the bytes sent.
func (l *loop) output(c chunk, most int) int {
	if c.data == nil {
		l.outputs--
		return 0
	}
	if most <= 0 {
		// The drain's budget is spent: drop the chunk, and send no empty
		// frame.
		return 0
	}
	data := c.data[:min(len(c.data), most)]
	l.send(&terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Output{
		Output: &terminalv1.Output{Stream: c.stream, Data: data},
	}})
	return len(data)
}

// report handles one report, and reports whether the shell has ended.
func (l *loop) report(ev reportEvent) bool {
	switch {
	case ev.err != nil:
		rule := RuleHelperGone
		if errors.Is(ev.err, ErrReport) {
			rule = RuleHelperReport
		}
		l.fail(rule)
		return true
	case ev.r.Final():
		l.exit(ev.r)
		return true
	case ev.r.Kind == ReportStarted:
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell program started", slog.Uint64("program_pid", uint64(ev.r.Value)))
	case ev.r.Kind == ReportStopped:
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell stopped", slog.String("decision", "report-stop"))
		l.send(&terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Stopped{Stopped: &terminalv1.Stopped{}}})
	case ev.r.Kind == ReportContinued:
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell continued")
	}
	return false
}

// exit sends what is still readable, within drainWait and drainBytes,
// and then the exit frame, as the last frame. A record after the exit
// report makes it a failure. The report comes from the project user's
// process, so it doesn't end the session by itself: processes of the
// session still running after it get the hangup, and the kill after
// the delay. When processes may be left after the kill, the last frame
// is the failure shell-hangup-kill, not the exit.
func (l *loop) exit(r Report) {
	if !l.drain() {
		l.fail(RuleHelperReport)
		return
	}
	if l.s.leftAfterExit() {
		l.s.log.LogAttrs(l.ctx, slog.LevelWarn, "shell processes left after the exit report",
			slog.String("rule", RuleKilled), slog.String("decision", "hangup-and-kill"))
		l.hangup("exit-with-processes-left")
		if l.s.waitForEnd(l.s.srv.cfg.KillDelay) && !l.s.kill() {
			l.killIncomplete()
			return
		}
	}
	exit := &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: r.Value}}
	attr := slog.Uint64("code", uint64(r.Value))
	if r.Kind == ReportSignaled {
		exit.Status = &terminalv1.Exit_Signal{Signal: r.Value}
		attr = slog.Uint64("signal", uint64(r.Value))
	}
	l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell exited", attr, slog.String("decision", "exit"))
	l.send(&terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: exit}})
}

// leftAfterExit waits up to commandWait for the helper to be reaped
// after its exit report, and then up to drainWait for the session to
// have no member left. It reports whether members may be left.
func (s *session) leftAfterExit() bool {
	t := time.NewTimer(commandWait)
	defer t.Stop()
	select {
	case <-s.gone:
	case <-t.C:
	}
	return s.waitForEnd(drainWait)
}

// drain relays output until each output stream and the reports have
// ended, or for drainWait, and reports false when a record came after
// the exit report.
func (l *loop) drain() bool {
	t := time.NewTimer(drainWait)
	defer t.Stop()
	budget, reportsEnded := drainBytes, false
	for l.outputs > 0 || !reportsEnded {
		select {
		case c := <-l.out:
			budget -= l.output(c, budget)
		case ev := <-l.reps:
			if !errors.Is(ev.err, io.EOF) {
				return false
			}
			reportsEnded = true
		case <-t.C:
			return true
		}
	}
	return true
}

// fail ends a shell whose helper can't be trusted or is gone: it hangs
// up, kills what is left of the session after the delay, and sends a
// failure frame with the helper's rule. When processes may be left after
// the kill, the failure is shell-hangup-kill instead, and the log line
// "shell helper failed" keeps the helper's rule.
func (l *loop) fail(rule string) {
	l.s.log.LogAttrs(l.ctx, slog.LevelWarn, "shell helper failed",
		slog.String("rule", rule), slog.String("decision", "hangup-and-kill"))
	l.hangup(rule)
	if l.s.waitForEnd(l.s.srv.cfg.KillDelay) && !l.s.kill() {
		l.killIncomplete()
		return
	}
	l.send(&terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Failure{
		Failure: &terminalv1.Failure{Rule: rule, Message: "the session helper failed"},
	}})
}

// streamEnded handles the end of the host's stream. After a link drop
// the stop loop has the shell's processes, and they stay as they are
// (S13-01-process-hardening, "When the link drops"). Otherwise the
// shell hangs up.
func (l *loop) streamEnded(err error) {
	l.frames, l.recvEnd = nil, nil
	if l.ctx.Err() == nil {
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell stream ended", slog.Any("error", err))
		l.hangupAndArm("stream-ended")
		return
	}
	l.stream = nil
	if l.s.srv.cfg.Dropped(l.ctx) {
		l.s.log.LogAttrs(l.ctx, slog.LevelWarn, "shell left to the stop loop",
			slog.String("decision", "leave-to-stop-loop"))
		return
	}
	l.hangupAndArm("stream-ended")
}

// hangupAndArm hangs up, and kills the shell's processes if no exit
// report comes within the delay.
func (l *loop) hangupAndArm(reason string) {
	if l.hungUp {
		return
	}
	l.hangup(reason)
	l.killAt = time.After(l.s.srv.cfg.KillDelay)
}

// hangup closes the controller in interactive use, so the kernel sends
// SIGHUP, or standard input otherwise, and sends the helper the hangup
// command, which sends SIGHUP and SIGCONT to the program's group
// (S06-09-session-lifecycle, "Closing the terminal ends the session").
func (l *loop) hangup(reason string) {
	if l.hungUp {
		return
	}
	l.hungUp = true
	l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell hangup",
		slog.String("reason", reason), slog.String("decision", "hangup"))
	closeAll(l.s.ctl, l.s.stdin)
	l.s.command(CommandHangup, 0)
	l.s.forget()
}

// frame handles a client frame other than input.
func (l *loop) frame(f *terminalv1.ClientFrame) {
	switch fr := f.GetFrame().(type) {
	case *terminalv1.ClientFrame_Resize:
		l.resize(fr.Resize)
	case *terminalv1.ClientFrame_Signal:
		l.signal(fr.Signal)
	case *terminalv1.ClientFrame_Stop:
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell stop", slog.String("decision", "stop"))
		l.s.command(CommandStop, 0)
	case *terminalv1.ClientFrame_Resume:
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell resume", slog.String("decision", "resume"))
		l.s.command(CommandResume, 0)
		l.s.forget()
	case *terminalv1.ClientFrame_StdoutClosed:
		l.stdoutClosed()
	case *terminalv1.ClientFrame_Hangup:
		l.hangupAndArm("hangup-frame")
	default:
		l.s.invalid(l.ctx, "unknown frame")
	}
}

func (l *loop) resize(size *terminalv1.WindowSize) {
	rows, cols, ok := windowSize(size)
	if l.s.ctl == nil || !ok {
		l.s.invalid(l.ctx, "resize")
		return
	}
	if err := sessionproc.SetSize(l.s.ctl, rows, cols); err != nil {
		l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell resize failed", slog.String("error", err.Error()))
	}
}

// frameSignals maps the stream's signals to the helper's.
var frameSignals = map[terminalv1.Signal]sessionproc.Signal{
	terminalv1.Signal_SIGNAL_INT:  sessionproc.SigInt,
	terminalv1.Signal_SIGNAL_QUIT: sessionproc.SigQuit,
	terminalv1.Signal_SIGNAL_TERM: sessionproc.SigTerm,
	terminalv1.Signal_SIGNAL_TSTP: sessionproc.SigTstp,
	terminalv1.Signal_SIGNAL_CONT: sessionproc.SigCont,
}

func (l *loop) signal(sig terminalv1.Signal) {
	s, ok := frameSignals[sig]
	if !ok {
		l.s.invalid(l.ctx, "signal")
		return
	}
	l.s.log.LogAttrs(l.ctx, slog.LevelInfo, "shell signal", slog.String("signal", s.String()))
	l.s.command(CommandSignal, s)
}

// stdoutClosed closes wb-guestd's read end of the program's standard
// output, so the program's next write fails with EPIPE or SIGPIPE, as
// on the host (S05-02-exit-status-and-signals, "A closed output").
func (l *loop) stdoutClosed() {
	if l.s.ctl != nil {
		l.s.invalid(l.ctx, "stdout closed in interactive use")
		return
	}
	_ = l.s.stdout.Close()
}

// command sends the helper a command.
func (s *session) command(c Command, sig sessionproc.Signal) {
	rec := EncodeCommand(c, sig)
	_ = s.commands.SetWriteDeadline(time.Now().Add(commandWait))
	if _, err := s.commands.Write(rec[:]); err != nil {
		s.log.LogAttrs(context.Background(), slog.LevelWarn, "shell command not sent",
			slog.Int("command", int(c)), slog.String("error", err.Error()))
	}
}

// invalid logs a client frame out of form, which is dropped.
func (s *session) invalid(ctx context.Context, what string) {
	s.log.LogAttrs(ctx, slog.LevelWarn, "shell frame dropped",
		slog.String("frame", what), slog.String("rule", RuleFrameInvalid), slog.String("decision", "drop"))
}
