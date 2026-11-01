package relay

import (
	"fmt"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// TestExitStatus is the table of S05-02-exit-status-and-signals, "Exit
// status", for `wb shell`: the program's code n gives n, its signal n
// gives 128 + n, and every failure of Wraith Box gives 255, with a last
// line on standard error that starts with `wraith box:` and names the
// rule.
func TestExitStatus(t *testing.T) {
	tests := []struct {
		name  string
		serve func(*fakeStream)
		code  int
		rule  string // the rule of the `wraith box:` line, or "" for none
	}{
		{"code 0", func(s *fakeStream) { s.serve(exitCode(0)) }, 0, ""},
		{"code 2", func(s *fakeStream) { s.serve(exitCode(2)) }, 2, ""},
		{"code 7", func(s *fakeStream) { s.serve(exitCode(7)) }, 7, ""},
		{"code 255", func(s *fakeStream) { s.serve(exitCode(255)) }, 255, ""},
		{"signal 9", func(s *fakeStream) { s.serve(exitSignal(9)) }, 137, ""},
		{"signal 64", func(s *fakeStream) { s.serve(exitSignal(64)) }, 192, ""},
		{"failure frame", func(s *fakeStream) { s.serve(failureFrame("guest-lost", "The guest stopped answering.")) }, 255, "guest-lost"},
		{"failure frame with a bad rule", func(s *fakeStream) { s.serve(failureFrame("Guest Lost", "x")) }, 255, RuleFrame},
		{"failure frame with a long message", func(s *fakeStream) { s.serve(failureFrame("x", strings.Repeat("a", 257))) }, 255, RuleFrame},
		{"closed stream", func(s *fakeStream) { close(s.frames) }, 255, RuleStreamClosed},
		{"broken stream", func(s *fakeStream) { s.frames <- received{err: errBroken} }, 255, RuleStreamBroken},
		{"exit code 256", func(s *fakeStream) { s.serve(exitCode(256)) }, 255, RuleExitRange},
		{"signal 0", func(s *fakeStream) { s.serve(exitSignal(0)) }, 255, RuleExitRange},
		{"signal 65", func(s *fakeStream) { s.serve(exitSignal(65)) }, 255, RuleExitRange},
		{"exit with no status", func(s *fakeStream) {
			s.serve(&terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{}}})
		}, 255, RuleExitRange},
		{"empty frame", func(s *fakeStream) { s.serve(&terminalv1.ServerFrame{}) }, 255, RuleFrame},
		{"nil frame", func(s *fakeStream) { s.serve(nil) }, 255, RuleFrame},
		{"output over 32 KiB", func(s *fakeStream) { s.serve(stdout(strings.Repeat("a", MaxFrameData+1))) }, 255, RuleFrame},
		{"output of no stream", func(s *fakeStream) { s.serve(outFrame(terminalv1.Stream_STREAM_UNSPECIFIED, "a")) }, 255, RuleFrame},
		{"standard error frame without a separate standard error", func(s *fakeStream) {
			s.serve(outFrame(terminalv1.Stream_STREAM_STDERR, "a"))
		}, 255, RuleFrame},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, allTTYs)
			h.start()
			tt.serve(h.stream)
			if code := h.wait(t); code != tt.code {
				t.Errorf("exit status %d, want %d", code, tt.code)
			}
			checkFailureLine(t, h, tt.rule)
		})
	}
}

// checkFailureLine checks that the last line on standard error is a
// `wraith box:` line that names rule, and that the log names it too, or
// with rule "" that no such line was written.
func checkFailureLine(t *testing.T, h *harness, rule string) {
	t.Helper()
	if rule == "" {
		if strings.Contains(h.e.text("stderr"), "wraith box:") {
			t.Errorf("standard error %q, want no wraith box: line", h.e.text("stderr"))
		}
		return
	}
	if last := h.lastLine(); !strings.HasPrefix(last, "wraith box: ") || !strings.HasSuffix(last, "(rule "+rule+")") {
		t.Errorf("last line %q, want a wraith box: line with rule %s", last, rule)
	}
	if !strings.Contains(h.logs.String(), "rule="+rule) {
		t.Errorf("log %q doesn't name rule %s", h.logs.String(), rule)
	}
}

// TestFailureLineStripped: a failure frame's message is guest text, so
// the `wraith box:` line holds none of the escaped set
// (S16-03-around-the-stream, "Guest text elsewhere").
func TestFailureLineStripped(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.start()
	h.stream.serve(failureFrame("guest-lost", "gone\x1b[2J\x1b]2;x\x07\u202e"))
	h.wait(t)
	if last := h.lastLine(); last != "wraith box: gone[2J]2;x (rule guest-lost)" {
		t.Errorf("last line %q", last)
	}
}

// TestRawMode: raw mode is on from the start of the relay to its end,
// in interactive use only (S05-01-agent-commands, "Interactive and
// non-interactive use").
func TestRawMode(t *testing.T) {
	for _, ttys := range []TTYs{allTTYs, {Stdin: true}, {Stdout: true}, {}} {
		t.Run(fmt.Sprintf("%+v", ttys), func(t *testing.T) {
			h := newHarness(t, ttys)
			h.start()
			h.stream.serve(stdout("hi"), exitCode(0))
			h.wait(t)
			raw, restore, out := h.e.index("raw", ""), h.e.index("restore", ""), h.e.index("stdout:", "hi")
			if !ttys.Interactive() {
				if raw != -1 || restore != -1 {
					t.Errorf("raw mode in non-interactive use: %q", h.e.all())
				}
				return
			}
			if raw == -1 || restore == -1 || raw >= out || restore <= out {
				t.Errorf("raw mode not around the output: %q", h.e.all())
			}
		})
	}
}

// TestPanicRestoresMode: a panic in the relay's goroutine or in a
// goroutine it started restores the terminal mode and ends with 255.
func TestPanicRestoresMode(t *testing.T) {
	t.Run("relay goroutine", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.term.panicSize = true
		h.start()
		h.e.waitOut(t, "wb: ")
		h.sigs <- tty.WindowChange
		if code := h.wait(t); code != ExitFailure {
			t.Errorf("exit status %d, want 255", code)
		}
		if h.e.index("restore", "") == -1 || !strings.Contains(h.lastLine(), RulePanic) {
			t.Errorf("events %q, last line %q", h.e.all(), h.lastLine())
		}
	})
	t.Run("receive goroutine", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.stream.panicRecv = true
		h.start()
		if code := h.wait(t); code != ExitFailure {
			t.Errorf("exit status %d, want 255", code)
		}
		if h.e.index("restore", "") == -1 || !strings.Contains(h.lastLine(), RulePanic) {
			t.Errorf("events %q, last line %q", h.e.all(), h.lastLine())
		}
	})
}

// TestTitle is S16-03-around-the-stream, "Title": the user's title is
// saved and wb's set before the relay, and restored at exit and before
// a stop, only when standard output is a terminal. The id is stripped
// of the escaped set.
func TestTitle(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.cfg.ID = "my\x07repo\x1b]0;evil"
	h.start()
	h.e.waitOut(t, "wb: ")
	h.stream.serve(stopped())
	h.e.waitOut(t, titleRestore)
	h.stream.next(t)
	if got := h.stream.next(t); kind(got) != "resume" {
		t.Fatalf("after the stop, frame %q, want resume", kind(got))
	}
	h.stream.serve(exitCode(0))
	h.wait(t)
	out := h.e.text("stdout")
	set := titleSave + "\x1b]2;wb: myrepo]0;evil\a"
	if !strings.HasPrefix(out, set) {
		t.Errorf("standard output starts %q, want %q", out, set)
	}
	if strings.Count(out, set) != 2 || strings.Count(out, titleRestore) != 2 {
		t.Errorf("standard output %q, want the title set and restored twice", out)
	}
	if !strings.HasSuffix(out, titleRestore) {
		t.Errorf("standard output %q doesn't end with the title restore", out)
	}

	h = newHarness(t, TTYs{Stdin: true, Stderr: true})
	h.start()
	h.stream.serve(stdout("x"), exitCode(0))
	h.wait(t)
	if out := h.e.text("stdout"); out != "x" {
		t.Errorf("standard output not a terminal: %q, want only the filtered output", out)
	}
}

// TestResetBeforeLine is S16-03-around-the-stream, "Reset on exit":
// the reset follows the last output, and comes before the summary and
// the `wraith box:` line.
func TestResetBeforeLine(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.start()
	h.stream.serve(stdout("\x1b[30;40m fake summary\x1b]52;c;eA==\x07"), failureFrame("guest-lost", "lost"))
	h.wait(t)
	last := h.e.index("stdout:", "fake summary")
	reset := h.e.index("stdout:", "\x1b[0m")
	line := h.e.index("stderr:", "wraith box:")
	summary := h.e.index("stderr:", "terminal: dropped 1 clipboard write")
	if last == -1 || reset <= last || summary <= reset || line <= summary {
		t.Errorf("order of output %d, reset %d, summary %d, line %d: %q", last, reset, summary, line, h.e.all())
	}
}

// TestDrain is S16-03-around-the-stream, "Input drain".
func TestDrain(t *testing.T) {
	t.Run("ends on the reply with ?", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.cfg.DrainTimeout = waitLimit
		h.start()
		h.stream.serve(exitCode(0))
		h.e.waitOut(t, drainQuery)
		h.typeIn("typed\x1b[?1;1R")
		h.wait(t)
		if h.e.index("stdout:", titleRestore) < h.e.index("stdout:", drainQuery) {
			t.Errorf("title restored before the drain: %q", h.e.all())
		}
	})
	t.Run("discards earlier input", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.cfg.DrainTimeout = waitLimit
		h.start()
		h.stream.serve(exitCode(0))
		h.e.waitOut(t, drainQuery)
		h.typeIn("late reply\x1b[2;3R")
		h.typeIn("\x1b[?2;3R")
		h.wait(t)
		h.stream.none(t, 10*time.Millisecond)
	})
	t.Run("not ended by a reply without ?", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.cfg.DrainTimeout = waitLimit
		h.start()
		h.stream.serve(exitCode(0))
		h.e.waitOut(t, drainQuery)
		h.typeIn("\x1b[1;1R")
		select {
		case <-h.done:
			t.Fatal("the drain ended on CSI 1 ; 1 R")
		case <-time.After(50 * time.Millisecond):
		}
		h.typeIn("\x1b[?1;1R")
		h.wait(t)
	})
	t.Run("ends after 100 ms", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.cfg.DrainTimeout = 0
		h.cfg.defaults()
		h.start()
		h.stream.serve(exitCode(0))
		h.e.waitOut(t, drainQuery)
		start := time.Now()
		h.wait(t)
		if d := time.Since(start); d < 90*time.Millisecond || d > 2*time.Second {
			t.Errorf("the drain took %v, want about 100 ms", d)
		}
	})
	t.Run("not without a terminal on standard input", func(t *testing.T) {
		h := newHarness(t, TTYs{Stdout: true, Stderr: true})
		h.start()
		h.stream.serve(exitCode(0))
		h.wait(t)
		if strings.Contains(h.e.text("stdout"), drainQuery) {
			t.Errorf("drain query without a terminal on standard input: %q", h.e.text("stdout"))
		}
	})
}

// TestForwarded is S05-02-exit-status-and-signals, "Forwarded" and
// "From the keyboard": SIGINT, SIGQUIT and SIGTERM become signal frames
// in both uses and wb keeps waiting, and SIGWINCH becomes a resize
// frame in interactive use.
func TestForwarded(t *testing.T) {
	for _, ttys := range []TTYs{allTTYs, {}} {
		h := newHarness(t, ttys)
		h.start()
		h.sigs <- tty.Interrupt
		h.sigs <- tty.Quit
		h.sigs <- tty.Terminate
		h.term.setSize(50, 132)
		h.sigs <- tty.WindowChange
		want := []string{"signal SIGNAL_INT", "signal SIGNAL_QUIT", "signal SIGNAL_TERM"}
		if ttys.Interactive() {
			want = append(want, "resize 50x132")
		}
		for _, w := range want {
			if got := kind(h.stream.next(t)); got != w {
				t.Errorf("%+v: frame %q, want %q", ttys, got, w)
			}
		}
		h.stream.serve(exitCode(130))
		if code := h.wait(t); code != 130 {
			t.Errorf("exit status %d, want 130", code)
		}
	}
}

// TestResizeOutOfRange: a window size out of 1 to 65535 isn't sent.
func TestResizeOutOfRange(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.start()
	h.term.setSize(0, 80)
	h.sigs <- tty.WindowChange
	h.term.setSize(24, 65536)
	h.sigs <- tty.WindowChange
	h.stream.none(t, 20*time.Millisecond)
	h.stream.serve(exitCode(0))
	h.wait(t)
	if !strings.Contains(h.logs.String(), "rule="+RuleWindowSize) {
		t.Errorf("log %q doesn't name the rule", h.logs.String())
	}
}

// TestHangup is S05-02-exit-status-and-signals, "Hangup": SIGHUP sends
// the hangup frame, wb waits for the last frame and exits by the table,
// and nothing more reaches the terminal, not even the reset.
func TestHangup(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.start()
	h.e.waitOut(t, "wb: ")
	before := len(h.e.text("stdout"))
	h.sigs <- tty.Hangup
	if got := kind(h.stream.next(t)); got != "hangup" {
		t.Fatalf("frame %q, want hangup", got)
	}
	h.typeIn("x")
	h.stream.serve(stdout("after"), exitCode(129))
	if code := h.wait(t); code != 129 {
		t.Errorf("exit status %d, want 129", code)
	}
	if after := h.e.text("stdout")[before:]; after != "" {
		t.Errorf("after SIGHUP the terminal got %q", after)
	}
	if s := h.e.text("stderr"); s != "" {
		t.Errorf("after SIGHUP standard error got %q", s)
	}
	h.stream.none(t, 10*time.Millisecond)
}

// TestClosedOutput is S05-02-exit-status-and-signals, "A closed
// output": EPIPE on standard output in non-interactive use sends the
// standard-output-closed frame, and wb exits as the program does.
func TestClosedOutput(t *testing.T) {
	h := newHarness(t, TTYs{})
	h.stdout.fail(fmt.Errorf("write /dev/stdout: %w", syscall.EPIPE))
	h.start()
	h.stream.serve(stdout("a"))
	if got := kind(h.stream.next(t)); got != "stdout-closed" {
		t.Fatalf("frame %q, want stdout-closed", got)
	}
	h.stream.serve(stdout("b"), exitCode(141))
	if code := h.wait(t); code != 141 {
		t.Errorf("exit status %d, want 141", code)
	}
	h.stream.none(t, 10*time.Millisecond)
}

// TestFailedOutput: a write that fails other than with EPIPE ends wb
// with 255 once the program exits, because its output was lost.
func TestFailedOutput(t *testing.T) {
	h := newHarness(t, TTYs{})
	h.stdout.fail(fmt.Errorf("write out.txt: %w", syscall.ENOSPC))
	h.start()
	h.stream.serve(stdout("a"))
	if got := kind(h.stream.next(t)); got != "stdout-closed" {
		t.Fatalf("frame %q, want stdout-closed", got)
	}
	h.stream.serve(exitCode(0))
	if code := h.wait(t); code != ExitFailure || !strings.Contains(h.lastLine(), RuleOutputWrite) {
		t.Errorf("exit status %d, last line %q", code, h.lastLine())
	}
}

// TestInput: input crosses unfiltered in frames of at most 32 KiB, and
// the end of standard input in non-interactive use sends the
// standard-input-closed frame.
func TestInput(t *testing.T) {
	h := newHarness(t, TTYs{})
	h.start()
	h.typeIn("\x1b[A\x03")
	if got := kind(h.stream.next(t)); got != "input \x1b[A\x03" {
		t.Errorf("frame %q", got)
	}
	big := strings.Repeat("a", MaxFrameData+10)
	h.typeIn(big)
	var n int
	for n < len(big) {
		f := h.stream.next(t)
		in := f.GetInput()
		if len(in) == 0 || len(in) > MaxFrameData {
			t.Fatalf("input frame of %d bytes", len(in))
		}
		n += len(in)
	}
	_ = h.stdin.Close()
	if got := kind(h.stream.next(t)); got != "stdin-closed" {
		t.Errorf("frame %q, want stdin-closed", got)
	}
	h.stream.serve(exitCode(0))
	h.wait(t)
}

// TestStdinEndInteractive: in interactive use the end of the terminal's
// input sends nothing; SIGHUP is how a closed terminal ends the
// session.
func TestStdinEndInteractive(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.start()
	_ = h.stdin.Close()
	h.stream.none(t, 20*time.Millisecond)
	h.stream.serve(exitCode(0))
	h.wait(t)
}

// TestSeparateStderr: standard error frames pass their own filter to
// standard error, in non-interactive use and in interactive use when
// standard error isn't a terminal (S16-01-filter, "Where").
func TestSeparateStderr(t *testing.T) {
	for _, ttys := range []TTYs{{}, {Stdin: true, Stdout: true}} {
		h := newHarness(t, ttys)
		h.start()
		h.stream.serve(outFrame(terminalv1.Stream_STREAM_STDERR, "err\x1b]2;t\x07\x1b[31m!"), exitCode(0))
		h.wait(t)
		if got := h.e.text("stderr"); !strings.HasPrefix(got, "err\x1b[31m!") || strings.Contains(got, "\x1b]2;") {
			t.Errorf("%+v: standard error %q", ttys, got)
		}
		if !strings.Contains(h.e.text("stderr"), "terminal: dropped 1 title change") {
			t.Errorf("%+v: no summary in %q", ttys, h.e.text("stderr"))
		}
	}
}

// TestStderrReset: when standard error is a terminal and got output, it
// gets its own filter's reset, also when standard output is a file.
func TestStderrReset(t *testing.T) {
	h := newHarness(t, TTYs{Stdin: true, Stderr: true})
	h.start()
	h.stream.serve(outFrame(terminalv1.Stream_STREAM_STDERR, "\x1b[7m"), exitCode(0))
	h.wait(t)
	if got := h.e.text("stderr"); !strings.Contains(got, "\x1b[7m\x1b[?2026l") {
		t.Errorf("standard error %q, want the reset after the output", got)
	}
}

// TestJobControlInteractive is S05-02-exit-status-and-signals, "Job
// control", in interactive use.
func TestJobControlInteractive(t *testing.T) {
	t.Run("stopped frame", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.start()
		h.stream.serve(stdout("\x1b[>1u"), stopped())
		if got := kind(h.stream.next(t)); got != "resize 24x80" {
			t.Fatalf("frame %q, want resize", got)
		}
		if got := kind(h.stream.next(t)); got != "resume" {
			t.Fatalf("frame %q, want resume", got)
		}
		checkStop(t, h)
		if !strings.Contains(h.e.text("stdout"), "\x1b[<1u") {
			t.Errorf("the stop reset didn't pop the guest's push: %q", h.e.text("stdout"))
		}
		h.stream.serve(exitCode(0))
		h.wait(t)
	})
	t.Run("outside SIGTSTP with a stopped frame", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.cfg.StopWait = waitLimit
		h.start()
		h.sigs <- tty.TerminalStop
		if got := kind(h.stream.next(t)); got != "stop" {
			t.Fatalf("frame %q, want stop", got)
		}
		h.stream.serve(stopped())
		h.stream.next(t)
		if got := kind(h.stream.next(t)); got != "resume" {
			t.Fatalf("frame %q, want resume", got)
		}
		checkStop(t, h)
		h.stream.serve(exitCode(0))
		h.wait(t)
	})
	t.Run("outside SIGTSTP without a stopped frame", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.cfg.StopWait = 30 * time.Millisecond
		h.start()
		h.sigs <- tty.TerminalStop
		if got := kind(h.stream.next(t)); got != "stop" {
			t.Fatalf("frame %q, want stop", got)
		}
		h.stream.next(t)
		if got := kind(h.stream.next(t)); got != "resume" {
			t.Fatalf("frame %q, want resume", got)
		}
		checkStop(t, h)
		h.stream.serve(exitCode(0))
		h.wait(t)
	})
}

// checkStop checks the order of a stop in interactive use: the reset
// and the drain, the user's title, cooked mode, the stop, then raw mode
// and wb's title again.
func checkStop(t *testing.T, h *harness) {
	t.Helper()
	steps := []struct{ prefix, sub string }{
		{"stdout:", "\x1b[?2026l"},
		{"stdout:", drainQuery},
		{"stdout:", titleRestore},
		{"restore", ""},
		{"stop-self", ""},
		{"raw", ""},
		{"stdout:", "\x1b]2;wb: "},
	}
	evs := h.e.all()
	i := h.e.index("stdout:", "\x1b[?2026l")
	for _, s := range steps {
		for i < len(evs) && (!strings.HasPrefix(evs[i], s.prefix) || !strings.Contains(evs[i], s.sub)) {
			i++
		}
		if i == len(evs) {
			t.Fatalf("no %s%q in order in %q", s.prefix, s.sub, evs)
		}
	}
}

// TestJobControlNonInteractive: in non-interactive use a SIGTSTP is
// forwarded before wb stops itself, and a SIGCONT after it continues.
// A stopped frame is ignored and logged.
func TestJobControlNonInteractive(t *testing.T) {
	h := newHarness(t, TTYs{})
	h.start()
	h.sigs <- tty.TerminalStop
	for _, w := range []string{"signal SIGNAL_TSTP", "signal SIGNAL_CONT"} {
		if got := kind(h.stream.next(t)); got != w {
			t.Fatalf("frame %q, want %q", got, w)
		}
	}
	stop := h.e.index("stop-self", "")
	if stop == -1 {
		t.Fatalf("wb didn't stop itself: %q", h.e.all())
	}
	h.stream.serve(stopped(), stdout("still here"), exitCode(0))
	h.wait(t)
	if h.e.index("stop-self", "") != stop || strings.Count(strings.Join(h.e.all(), ""), "stop-self") != 1 {
		t.Errorf("a stopped frame stopped wb in non-interactive use: %q", h.e.all())
	}
	if !strings.Contains(h.logs.String(), "rule="+RuleStoppedIgnored) {
		t.Errorf("log %q doesn't name the rule", h.logs.String())
	}
}

// TestSendFailure: a failed send doesn't end the relay. wb sends
// nothing more and reads on, so the frame that ends the stream decides
// the exit status: the exit frame wb-hostd queued before it ended the
// call, or 255 for a stream that ends without one.
func TestSendFailure(t *testing.T) {
	t.Run("exit frame after io.EOF", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.stream.sendErr = io.EOF
		h.start()
		h.sigs <- tty.Interrupt
		// The loop picks at random among ready channels, so the exit
		// frame is served only after the send failed.
		if got := kind(h.stream.nextRefused(t)); got != "signal SIGNAL_INT" {
			t.Fatalf("failed send of %q, want the interrupt", got)
		}
		h.sigs <- tty.Quit
		h.stream.serve(exitCode(7))
		if code := h.wait(t); code != 7 {
			t.Errorf("exit status %d, want 7; last line %q", code, h.lastLine())
		}
		if !strings.Contains(h.logs.String(), "send failed") {
			t.Errorf("log %q doesn't record the failed send", h.logs.String())
		}
		// Nothing is sent after the failed send: no frame for the quit,
		// if the loop took it before the exit frame.
		if n := len(h.stream.refused); n != 0 || h.stream.refusedOverflow.Load() {
			t.Errorf("%d more sends after the failed one, buffer overflowed: %v", n, h.stream.refusedOverflow.Load())
		}
	})
	t.Run("stream closed without an exit frame", func(t *testing.T) {
		h := newHarness(t, allTTYs)
		h.stream.sendErr = errBroken
		h.start()
		h.sigs <- tty.Interrupt
		if got := kind(h.stream.nextRefused(t)); got != "signal SIGNAL_INT" {
			t.Fatalf("failed send of %q, want the interrupt", got)
		}
		close(h.stream.frames)
		if code := h.wait(t); code != ExitFailure || !strings.Contains(h.lastLine(), RuleStreamClosed) {
			t.Errorf("exit status %d, last line %q", code, h.lastLine())
		}
	})
}

// TestNoTerminal: interactive use without a terminal to set up fails
// before the relay with 255, at once: no drain waits for input that no
// goroutine reads.
func TestNoTerminal(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.cfg.Terminal = nil
	h.cfg.DrainTimeout = waitLimit
	start := time.Now()
	h.start()
	if code := h.wait(t); code != ExitFailure || !strings.Contains(h.lastLine(), RuleTerminal) {
		t.Errorf("exit status %d, last line %q", code, h.lastLine())
	}
	if d := time.Since(start); d > waitLimit/2 {
		t.Errorf("a relay that didn't start took %v", d)
	}
}

// TestJobControlNonInteractiveTerminal: in non-interactive use with
// standard output a terminal, wb writes the stop reset and restores the
// user's title before it stops itself, and sets its title again after
// it continues (S16-03-around-the-stream, "Around the stream").
func TestJobControlNonInteractiveTerminal(t *testing.T) {
	h := newHarness(t, TTYs{Stdout: true, Stderr: true})
	h.start()
	h.stream.serve(stdout("\x1b[>1u"))
	h.e.waitOut(t, "\x1b[>1u")
	h.sigs <- tty.TerminalStop
	for _, w := range []string{"signal SIGNAL_TSTP", "signal SIGNAL_CONT"} {
		if got := kind(h.stream.next(t)); got != w {
			t.Fatalf("frame %q, want %q", got, w)
		}
	}
	pop := h.e.index("stdout:", "\x1b[<1u")
	restore := h.e.index("stdout:", titleRestore)
	stop := h.e.index("stop-self", "")
	evs := h.e.all()
	again := -1
	for i := stop + 1; stop >= 0 && i < len(evs); i++ {
		if strings.Contains(evs[i], "\x1b]2;wb: ") {
			again = i
			break
		}
	}
	if pop == -1 || restore <= pop || stop <= restore || again == -1 {
		t.Errorf("order of pop %d, title restore %d, stop %d, title again %d: %q", pop, restore, stop, again, evs)
	}
	if strings.Contains(h.e.text("stdout"), drainQuery) {
		t.Error("a drain without a terminal on standard input")
	}
	h.stream.serve(exitCode(0))
	h.wait(t)
}

// TestLateStopped: after wb stopped itself because no stopped frame
// came in time, the late one is ignored, so wb doesn't stop twice. A
// stopped frame after guest output stops wb again.
func TestLateStopped(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.cfg.StopWait = 10 * time.Millisecond
	h.start()
	h.sigs <- tty.TerminalStop
	for _, w := range []string{"stop", "resize 24x80", "resume"} {
		if got := kind(h.stream.next(t)); got != w {
			t.Fatalf("frame %q, want %q", got, w)
		}
	}
	h.stream.serve(stopped())
	h.stream.none(t, 20*time.Millisecond)
	h.stream.serve(stdout("redrawn"), stopped())
	for _, w := range []string{"resize 24x80", "resume"} {
		if got := kind(h.stream.next(t)); got != w {
			t.Fatalf("frame %q, want %q", got, w)
		}
	}
	h.stream.serve(exitCode(0))
	h.wait(t)
	if n := strings.Count(strings.Join(h.e.all(), "\n"), "stop-self"); n != 2 {
		t.Errorf("wb stopped itself %d times, want 2", n)
	}
}

// TestHangupPipedInput: after SIGHUP, input from a pipe and its end
// still reach the guest in non-interactive use.
func TestHangupPipedInput(t *testing.T) {
	h := newHarness(t, TTYs{})
	h.start()
	h.sigs <- tty.Hangup
	if got := kind(h.stream.next(t)); got != "hangup" {
		t.Fatalf("frame %q, want hangup", got)
	}
	h.typeIn("data")
	if got := kind(h.stream.next(t)); got != "input data" {
		t.Fatalf("frame %q, want the input", got)
	}
	_ = h.stdin.Close()
	if got := kind(h.stream.next(t)); got != "stdin-closed" {
		t.Fatalf("frame %q, want stdin-closed", got)
	}
	h.stream.serve(exitCode(0))
	h.wait(t)
}

// TestReportOwnLine: when the guest's last standard error byte doesn't
// end a line, as on a pipe or a file, the report starts a line of its
// own, so the `wraith box:` line holds no guest text.
func TestReportOwnLine(t *testing.T) {
	h := newHarness(t, TTYs{})
	h.start()
	h.stream.serve(outFrame(terminalv1.Stream_STREAM_STDERR, "guest says"), failureFrame("guest-lost", "lost"))
	h.wait(t)
	if got := h.e.text("stderr"); got != "guest says\nwraith box: lost (rule guest-lost)\n" {
		t.Errorf("standard error %q", got)
	}
}

// TestNotAFrame: a ShellService response that isn't a server frame is
// out of bounds.
func TestNotAFrame(t *testing.T) {
	h := newHarness(t, allTTYs)
	h.start()
	h.stream.frames <- received{err: errNotFrame}
	if code := h.wait(t); code != ExitFailure || !strings.Contains(h.lastLine(), RuleFrame) {
		t.Errorf("exit status %d, last line %q", code, h.lastLine())
	}
}
