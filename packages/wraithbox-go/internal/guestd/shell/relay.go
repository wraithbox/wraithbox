package shell

import (
	"log/slog"
	"os"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// The goroutines of a running shell. Each hands what it reads to the
// loop's goroutine, and returns when the loop is done.

// readReports reads the helper's reports until they end.
func (l *loop) readReports() {
	rr := NewReportReader(l.s.reports)
	for {
		r, err := rr.Next()
		select {
		case l.reps <- reportEvent{r: r, err: err}:
		case <-l.done:
			return
		}
		if err != nil {
			return
		}
	}
}

// readOutput reads one output stream in chunks of at most chunkSize
// until it ends or is closed.
func (l *loop) readOutput(f *os.File, stream terminalv1.Stream) {
	for {
		buf := make([]byte, chunkSize)
		n, err := f.Read(buf)
		if n > 0 {
			select {
			case l.out <- chunk{stream: stream, data: buf[:n]}:
			case <-l.done:
				return
			}
		}
		if err != nil {
			select {
			case l.out <- chunk{stream: stream}:
			case <-l.done:
			}
			return
		}
	}
}

// recv reads the host's frames. Input and the close of standard input go
// to writeInput in order, and the other frames to the loop.
// The loop drops its copies of frames and end once the stream ended, so
// recv takes its own. With inputQueue frames waiting for a program that
// doesn't read them, recv waits, and the frames behind them wait too: a
// hangup among them is handled once the queue has room. The end of the
// stream doesn't wait, and hangs the shell up.
func (l *loop) recv(stream Stream, frames chan<- *terminalv1.ClientFrame, end chan<- error) {
	defer close(l.input)
	for {
		req, err := stream.Recv()
		if err != nil {
			end <- err
			return
		}
		f := req.GetFrame()
		to := frames
		switch f.GetFrame().(type) {
		case *terminalv1.ClientFrame_Input, *terminalv1.ClientFrame_StdinClosed:
			to = l.input
		}
		select {
		case to <- f:
		case <-l.ctx.Done():
			end <- l.ctx.Err()
			return
		case <-l.done:
			return
		}
	}
}

// writeInput writes input to the program, and closes its standard input
// on stdin_closed. A write that fails is dropped: the program closed its
// standard input.
func (l *loop) writeInput() {
	for f := range l.input {
		if f.GetStdinClosed() != nil {
			l.closeStdin()
			continue
		}
		if _, err := l.s.stdin.Write(f.GetInput()); err != nil {
			l.s.log.LogAttrs(l.ctx, slog.LevelDebug, "shell input dropped", slog.String("error", err.Error()))
		}
	}
}

func (l *loop) closeStdin() {
	if l.s.ctl != nil {
		l.s.invalid(l.ctx, "stdin closed in interactive use")
		return
	}
	_ = l.s.stdin.Close()
}
