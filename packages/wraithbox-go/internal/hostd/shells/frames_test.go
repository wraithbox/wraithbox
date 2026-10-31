package shells

import (
	"strings"
	"testing"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

func outFrame(s terminalv1.Stream, data []byte) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Output{Output: &terminalv1.Output{Stream: s, Data: data}}}
}

func exitCode(c uint32) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: c}}}}
}

func exitSignal(s uint32) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Signal{Signal: s}}}}
}

func stoppedFrame() *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Stopped{Stopped: &terminalv1.Stopped{}}}
}

func failFrame(rule, msg string) *terminalv1.ServerFrame {
	return &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Failure{Failure: &terminalv1.Failure{Rule: rule, Message: msg}}}
}

// TestCheckFrame feeds the checker each kind of frame in and out of its
// bounds, and sees each one out of bounds fail closed with its rule.
func TestCheckFrame(t *testing.T) {
	interactive := &terminalv1.Setup{Interactive: true}
	piped := &terminalv1.Setup{}
	separate := &terminalv1.Setup{SeparateStderr: true}
	for _, tc := range []struct {
		name  string
		setup *terminalv1.Setup
		frame *terminalv1.ServerFrame
		rule  string // "" when accepted
	}{
		{"stdout", piped, outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("x")), ""},
		{"stdout at the cap", piped, outFrame(terminalv1.Stream_STREAM_STDOUT, make([]byte, MaxFrameData)), ""},
		{"stdout over the cap", piped, outFrame(terminalv1.Stream_STREAM_STDOUT, make([]byte, MaxFrameData+1)), RuleFrame},
		{"stderr asked for", separate, outFrame(terminalv1.Stream_STREAM_STDERR, []byte("x")), ""},
		{"stderr not asked for", piped, outFrame(terminalv1.Stream_STREAM_STDERR, []byte("x")), RuleFrame},
		{"stream unset", piped, outFrame(terminalv1.Stream_STREAM_UNSPECIFIED, []byte("x")), RuleFrame},
		{"stream unknown", separate, outFrame(terminalv1.Stream(9), []byte("x")), RuleFrame},
		{"stopped interactive", interactive, stoppedFrame(), ""},
		{"stopped piped", piped, stoppedFrame(), RuleFrame},
		{"exit 0", piped, exitCode(0), ""},
		{"exit 255", piped, exitCode(255), ""},
		{"exit 256", piped, exitCode(256), RuleExitStatus},
		{"signal 1", piped, exitSignal(1), ""},
		{"signal 64", piped, exitSignal(64), ""},
		{"signal 0", piped, exitSignal(0), RuleExitStatus},
		{"signal 65", piped, exitSignal(65), RuleExitStatus},
		{"exit unset", piped, &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{Exit: &terminalv1.Exit{}}}, RuleExitStatus},
		{"failure", piped, failFrame("guest-x", "boom"), RuleFailed},
		{"failure rule empty", piped, failFrame("", "boom"), RuleFrame},
		{"failure rule upper", piped, failFrame("Guest", "boom"), RuleFrame},
		{"failure rule long", piped, failFrame(strings.Repeat("a", maxRule+1), "boom"), RuleFrame},
		{"failure message long", piped, failFrame("guest-x", strings.Repeat("a", maxMessage+1)), RuleFrame},
		{"unset", piped, &terminalv1.ServerFrame{}, RuleFrame},
		{"nil", piped, nil, RuleFrame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, fail := NewChecker(tc.setup).Check(tc.frame)
			if tc.rule == "" {
				if fail != nil || out == nil {
					t.Fatalf("Check = %v, %+v; want accepted", out, fail)
				}
				return
			}
			if fail == nil || fail.Rule != tc.rule || out != nil {
				t.Fatalf("Check = %v, %+v; want rule %s", out, fail, tc.rule)
			}
		})
	}
}

// TestCheckAfterLast sees every frame after an exit or a failure refused.
func TestCheckAfterLast(t *testing.T) {
	for _, last := range []*terminalv1.ServerFrame{exitCode(0), exitCode(300), failFrame("x", "y"), {}} {
		c := NewChecker(&terminalv1.Setup{})
		_, _ = c.Check(last)
		out, fail := c.Check(exitCode(0))
		if out != nil || fail == nil || fail.Rule != RuleFrame {
			t.Fatalf("after %v: Check = %v, %+v", last, out, fail)
		}
	}
}

// TestStoppedRepeat sees a stopped frame before wb's resume dropped and
// counted, and one after it passed on.
func TestStoppedRepeat(t *testing.T) {
	c := NewChecker(&terminalv1.Setup{Interactive: true})
	if out, fail := c.Check(stoppedFrame()); out == nil || fail != nil {
		t.Fatalf("first stopped: %v, %+v", out, fail)
	}
	if out, fail := c.Check(stoppedFrame()); out != nil || fail != nil {
		t.Fatalf("repeated stopped: %v, %+v", out, fail)
	}
	if got := c.Repeats(); got != 1 {
		t.Fatalf("Repeats = %d", got)
	}
	c.Resumed()
	if out, fail := c.Check(stoppedFrame()); out == nil || fail != nil {
		t.Fatalf("stopped after resume: %v, %+v", out, fail)
	}
}

// TestFrameRebuilt sees that an accepted frame is a new one, without the
// guest's unknown fields.
func TestFrameRebuilt(t *testing.T) {
	in := outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("x"))
	in.ProtoReflect().SetUnknown([]byte{0xf8, 0x01, 0x01})
	out, fail := NewChecker(&terminalv1.Setup{}).Check(in)
	if fail != nil || out == in || len(out.ProtoReflect().GetUnknown()) != 0 {
		t.Fatalf("Check = %v, %+v", out, fail)
	}
}

func TestQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"boom", `the guest reported: "boom"`},
		{"\x1b[2J clear\u202e", `the guest reported: "[2J clear"`},
		{"\x9b fail", `the guest reported: " fail"`},
	} {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := Quote(strings.Repeat("é", maxMessage))
	if len(long) > maxMessage || !strings.HasSuffix(long, `é"`) {
		t.Fatalf("Quote of a long message: %d bytes, %q", len(long), long[len(long)-4:])
	}
}

func TestLogText(t *testing.T) {
	if got := LogText("a\x1bb"); got != `"a\x1bb"` {
		t.Fatalf("LogText = %s", got)
	}
	if got := LogText(strings.Repeat("a", 100)); len(got) != maxLogged+2 {
		t.Fatalf("LogText of 100 bytes = %d bytes", len(got))
	}
}
