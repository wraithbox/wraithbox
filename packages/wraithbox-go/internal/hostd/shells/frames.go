package shells

import (
	"fmt"
	"strconv"
	"sync"
	"unicode/utf8"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
)

// The bounds of wraithbox.terminal.v1 that wb-hostd checks on a guest
// frame (proto/wraithbox/terminal/v1/terminal.proto;
// SEC11-root-gains-nothing).
const (
	// MaxFrameData is the most bytes of one output frame.
	MaxFrameData = 32 << 10
	// maxExitCode is the largest exit code, and maxSignal the largest
	// signal number (S06-09-session-lifecycle, "Ending").
	maxExitCode = 255
	maxSignal   = 64
	// maxRule and maxMessage bound a failure frame.
	maxRule    = 64
	maxMessage = 256
	// maxLogged is how much of a guest's text the log keeps.
	maxLogged = 64
)

// The rules of a shell's failures, in the failure frame wb gets and in
// the log.
const (
	// RuleFrame: a guest frame that is unset, of a kind this build
	// doesn't know, out of its bounds, out of order, or after the last
	// one.
	RuleFrame = "guest-shell-frame"
	// RuleExitStatus: an exit with a code over 255, a signal outside 1 to
	// 64, or neither (S06-09-session-lifecycle, "Ending").
	RuleExitStatus = "guest-exit-status"
	// RuleRefused: wb-guestd refused the shell's start.
	RuleRefused = "guest-shell-refused"
	// RuleFailed: wb-guestd sent a failure frame within its bounds. wb
	// gets the guest's message stripped and quoted, never as it was
	// written.
	RuleFailed = "guest-shell-failed"
	// RuleClosed: the guest stream ended or failed before the exit, with
	// the link up.
	RuleClosed = "guest-shell-closed"
	// RuleLost: the link to wb-guestd dropped under the shell, or the VM
	// stopped (S06-09-session-lifecycle: no resume in V1-M2).
	RuleLost = "shell-lost"
	// RuleStoppedRepeat: a stopped frame before the resume of the last
	// one, dropped and counted.
	RuleStoppedRepeat = "guest-stopped-repeat"
	// RuleHangupTimeout: the guest sent no exit within the hangup delay
	// and its margin after the hangup, and the stream was cancelled.
	RuleHangupTimeout = "shell-hangup-timeout"
)

// Failure is a shell's end as a Wraith Box failure: the rule, the text
// for wb, which holds no guest text but quoted and stripped, and a
// detail for the log only.
type Failure struct {
	Rule    string
	Message string
	Detail  string
}

// Frame returns the failure frame wb gets.
func (f *Failure) Frame() *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Failure{Failure: &terminalv1.Failure{Rule: f.Rule, Message: f.Message}}}
}

// Checker checks the frames of one shell's guest stream, in order, and
// builds the frame wb gets from each it accepts, so no unknown field
// from the guest reaches wb (SEC11-root-gains-nothing). The guest
// stream's receiver calls Check, and the client stream's receiver
// calls Resumed, so its methods are safe for concurrent use.
type Checker struct {
	interactive    bool
	separateStderr bool

	mu sync.Mutex
	// stopped is set by an accepted stopped frame, and cleared by the
	// next resume from wb.
	stopped bool
	// repeats counts the stopped frames dropped as repeats.
	repeats int
	// last is set once Check returned an exit or a failure.
	last bool
}

// NewChecker returns the checker of a shell started with setup.
func NewChecker(setup *terminalv1.Setup) *Checker {
	return &Checker{interactive: setup.GetInteractive(), separateStderr: setup.GetSeparateStderr()}
}

// Resumed records that wb sent a resume or SIGCONT, after which the
// guest may report a stop again.
func (c *Checker) Resumed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = false
}

// Repeats returns the count of stopped frames dropped as repeats.
func (c *Checker) Repeats() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.repeats
}

// Check checks the guest frame f. It returns the frame for wb when f is
// accepted, nil and nil when f is dropped (a repeated stopped frame),
// and the failure that ends the shell otherwise. An exit or a failure
// is the last frame: every frame after it is a failure, and Check
// never returns a frame for wb after it.
func (c *Checker) Check(f *terminalv1.ServerFrame) (*terminalv1.ServerFrame, *Failure) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last {
		return nil, frameFailure("a frame after the last one")
	}
	out, fail := c.check(f)
	if fail != nil || out.GetExit() != nil {
		c.last = true
	}
	return out, fail
}

func (c *Checker) check(f *terminalv1.ServerFrame) (*terminalv1.ServerFrame, *Failure) {
	switch frame := f.GetFrame().(type) {
	case *terminalv1.ServerFrame_Output:
		return c.output(frame.Output)
	case *terminalv1.ServerFrame_Stopped:
		return c.stop()
	case *terminalv1.ServerFrame_Exit:
		return checkExit(frame.Exit)
	case *terminalv1.ServerFrame_Failure:
		return nil, guestFailure(frame.Failure)
	}
	// Unset, or a kind this build doesn't know.
	return nil, frameFailure("a frame of no known kind")
}

// output accepts at most MaxFrameData bytes on standard output, or on
// standard error when the setup asked for its own frames.
func (c *Checker) output(o *terminalv1.Output) (*terminalv1.ServerFrame, *Failure) {
	data := o.GetData()
	if len(data) > MaxFrameData {
		return nil, frameFailure(fmt.Sprintf("an output frame of %d bytes", len(data)))
	}
	s := o.GetStream()
	switch {
	case s == terminalv1.Stream_STREAM_STDOUT:
	case s == terminalv1.Stream_STREAM_STDERR && c.separateStderr:
	default:
		return nil, frameFailure(fmt.Sprintf("output on stream %d", s))
	}
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Output{Output: &terminalv1.Output{Stream: s, Data: data}}}, nil
}

// stop accepts a stopped frame only in interactive use, and drops a
// repeat before wb's next resume.
func (c *Checker) stop() (*terminalv1.ServerFrame, *Failure) {
	if !c.interactive {
		return nil, frameFailure("a stopped frame in non-interactive use")
	}
	if c.stopped {
		c.repeats++
		return nil, nil
	}
	c.stopped = true
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Stopped{Stopped: &terminalv1.Stopped{}}}, nil
}

// checkExit accepts an exit code of 0 to 255 or a signal of 1 to 64
// (S06-09-session-lifecycle, "Ending").
func checkExit(e *terminalv1.Exit) (*terminalv1.ServerFrame, *Failure) {
	out := &terminalv1.Exit{}
	switch s := e.GetStatus().(type) {
	case *terminalv1.Exit_Code:
		if s.Code > maxExitCode {
			return nil, exitFailure(fmt.Sprintf("exit code %d", s.Code))
		}
		out.Status = &terminalv1.Exit_Code{Code: s.Code}
	case *terminalv1.Exit_Signal:
		if s.Signal < 1 || s.Signal > maxSignal {
			return nil, exitFailure(fmt.Sprintf("signal %d", s.Signal))
		}
		out.Status = &terminalv1.Exit_Signal{Signal: s.Signal}
	default:
		return nil, exitFailure("no code and no signal")
	}
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: out}}, nil
}

func frameFailure(detail string) *Failure {
	return &Failure{Rule: RuleFrame, Message: "wb-guestd sent a frame out of the stream's bounds, and the shell was ended", Detail: detail}
}

func exitFailure(detail string) *Failure {
	return &Failure{Rule: RuleExitStatus, Message: "wb-guestd reported an exit status out of range: " + detail, Detail: detail}
}

// guestFailure words a guest's failure frame for wb. A frame out of its
// bounds is a frame failure. One within them gives RuleFailed, with the
// guest's message stripped of the escaped set and quoted, cut to fit
// maxMessage (terminal.proto, Failure). The guest's rule goes to the
// log only.
func guestFailure(f *terminalv1.Failure) *Failure {
	if !validRule(f.GetRule()) || len(f.GetMessage()) > maxMessage {
		return frameFailure("a failure frame out of bounds: rule " + LogText(f.GetRule()))
	}
	return &Failure{Rule: RuleFailed, Message: Quote(f.GetMessage()), Detail: "guest rule " + f.GetRule()}
}

// quotePrefix and quoteSuffix wrap a guest message in a failure frame.
const (
	quotePrefix = `the guest reported: "`
	quoteSuffix = `"`
)

// Quote returns `the guest reported: "<msg>"`, with msg stripped of the
// escaped set of S16-03-around-the-stream ("Guest text elsewhere") and
// cut on a UTF-8 boundary so the whole is at most maxMessage bytes.
func Quote(msg string) string {
	s := guesttext.Strip(msg)
	room := maxMessage - len(quotePrefix) - len(quoteSuffix)
	if len(s) > room {
		s = s[:room]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return quotePrefix + s + quoteSuffix
}

// LogText shows a guest's text in the log: cut to maxLogged bytes, then
// quoted with every byte outside printable ASCII escaped.
func LogText(s string) string {
	if len(s) > maxLogged {
		s = s[:maxLogged]
	}
	return strconv.QuoteToASCII(s)
}

// validRule reports whether s is 1 to 64 bytes of lowercase letters,
// digits and hyphens.
func validRule(s string) bool {
	if s == "" || len(s) > maxRule {
		return false
	}
	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
