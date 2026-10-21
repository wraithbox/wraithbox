package relay

import (
	"errors"
	"fmt"
	"io"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// Bounds of wraithbox.terminal.v1 that wb checks
// (proto/wraithbox/terminal/v1/terminal.proto).
const (
	// MaxFrameData is the most bytes of one input or output frame.
	MaxFrameData = 32 << 10
	// maxWindow is the largest window dimension.
	maxWindow = 65535
	// maxExitCode is the largest exit code.
	maxExitCode = 255
	// maxSignal is the largest signal number.
	maxSignal = 64
	// maxRule and maxMessage bound a failure frame.
	maxRule    = 64
	maxMessage = 256
)

// The rules the relay decides by. Each failure line and each log record
// of a decision names one.
const (
	// RuleStreamClosed: the stream closed before its last frame.
	RuleStreamClosed = "relay-stream-closed"
	// RuleStreamBroken: the stream failed before its last frame.
	RuleStreamBroken = "relay-stream-broken"
	// RuleFrame: a server frame out of the bounds of
	// wraithbox.terminal.v1.
	RuleFrame = "relay-frame-bounds"
	// RuleExitRange: an exit frame with a code or signal out of range.
	RuleExitRange = "relay-exit-range"
	// RuleOutputWrite: writing the program's output failed.
	RuleOutputWrite = "relay-output-write"
	// RulePanic: the relay itself failed.
	RulePanic = "relay-panic"
	// RuleTerminal: wb's terminal can't be set up.
	RuleTerminal = "relay-terminal"
	// RuleWindowSize: a window size out of range is left out.
	RuleWindowSize = "relay-window-size"
	// RuleStdoutClosed: standard output's reader closed it.
	RuleStdoutClosed = "relay-stdout-closed"
	// RuleStoppedIgnored: a stopped frame in non-interactive use.
	RuleStoppedIgnored = "relay-stopped-ignored"
	// RuleEnvValue: an environment value left out of the setup.
	RuleEnvValue = "relay-env-value"
)

// failure is a Wraith Box failure that ends wb with 255.
type failure struct {
	rule    string
	message string
}

// line is the last line on standard error, which starts with
// `wraith box:` (S05-02-exit-status-and-signals, "Exit status"). The
// message may hold guest text, so it is stripped of the escaped set
// (S16-03-around-the-stream, "Guest text elsewhere").
func (f *failure) line() string {
	msg := guesttext.Strip(f.message)
	if msg == "" {
		msg = "failed"
	}
	return fmt.Sprintf("wraith box: %s (rule %s)", msg, guesttext.Strip(f.rule))
}

// end is how the relay ended: wb's exit status, and the failure when it
// is 255 because of Wraith Box.
type end struct {
	code int
	fail *failure
}

func frameEnd(detail string) end {
	return end{code: ExitFailure, fail: &failure{rule: RuleFrame, message: "wb-hostd sent " + detail}}
}

func streamEnd(err error) end {
	if errors.Is(err, io.EOF) {
		return end{code: ExitFailure, fail: &failure{rule: RuleStreamClosed, message: "the connection to wb-hostd closed before the program exited"}}
	}
	return end{code: ExitFailure, fail: &failure{rule: RuleStreamBroken, message: "the connection to wb-hostd broke: " + err.Error()}}
}

// exitEnd reads an exit frame by the table of
// S05-02-exit-status-and-signals: code n gives n, signal n gives 128 +
// n, and anything out of range gives 255.
func exitEnd(e *terminalv1.Exit) end {
	switch s := e.GetStatus().(type) {
	case *terminalv1.Exit_Code:
		if s.Code > maxExitCode {
			return exitRange(fmt.Sprintf("exit code %d", s.Code))
		}
		return end{code: int(s.Code)}
	case *terminalv1.Exit_Signal:
		if s.Signal < 1 || s.Signal > maxSignal {
			return exitRange(fmt.Sprintf("signal %d", s.Signal))
		}
		return end{code: 128 + int(s.Signal)}
	}
	return exitRange("no code and no signal")
}

func exitRange(detail string) end {
	return end{code: ExitFailure, fail: &failure{rule: RuleExitRange, message: "the guest reported an exit status out of range: " + detail}}
}

// failureEnd reads a failure frame. Its rule must be 1 to 64 bytes of
// lowercase letters, digits and hyphens, and its message at most 256
// bytes, or the frame is out of bounds. Either way wb exits with 255.
func failureEnd(f *terminalv1.Failure) end {
	if !validRule(f.GetRule()) || len(f.GetMessage()) > maxMessage {
		return frameEnd("a failure frame out of bounds")
	}
	return end{code: ExitFailure, fail: &failure{rule: f.GetRule(), message: f.GetMessage()}}
}

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
