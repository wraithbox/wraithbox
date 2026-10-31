package shells

import (
	"encoding/binary"
	"testing"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
)

// FuzzGuestShellFrame feeds arbitrary bytes as a sequence of
// GuestService.Shell responses through the decoding and the Checker
// that the relay uses (C10-terminal). Each response is a 2-byte
// big-endian length and that many bytes. The properties:
//
//   - nothing panics;
//   - no output frame for wb holds more than MaxFrameData bytes, or
//     standard error when the setup didn't ask for its own frames;
//   - no exit for wb has a code over 255 or a signal outside 1 to 64;
//   - no stopped frame reaches wb outside interactive use;
//   - every failure has a known rule, and a message of at most 256 bytes
//     of valid UTF-8 with no byte of the escaped set;
//   - once an exit or a failure is returned, every later frame is a
//     failure and nothing more reaches wb;
//   - a frame for wb re-encodes to a message that decodes to itself, and
//     holds no unknown field.
func FuzzGuestShellFrame(f *testing.F) {
	for _, seed := range [][]*terminalv1.ServerFrame{
		{outFrame(terminalv1.Stream_STREAM_STDOUT, []byte("hi\n")), exitCode(0)},
		{stoppedFrame(), stoppedFrame(), exitSignal(9)},
		{exitCode(256)},
		{exitSignal(0), exitCode(1)},
		{failFrame("guest-x", "boom\x1b[2J"), exitCode(0)},
		{outFrame(terminalv1.Stream_STREAM_STDERR, []byte("e")), {}},
	} {
		f.Add(encodeSeq(f, seed), byte(0))
		f.Add(encodeSeq(f, seed), byte(3))
	}
	f.Fuzz(func(t *testing.T, data []byte, flags byte) {
		setup := &terminalv1.Setup{Interactive: flags&1 != 0, SeparateStderr: flags&2 != 0}
		c := NewChecker(setup)
		ended := false
		for _, raw := range splitSeq(data) {
			var resp guestv1.ShellResponse
			if proto.Unmarshal(raw, &resp) == nil {
				ended = checkStep(t, c, setup, resp.GetFrame(), ended)
			}
		}
	})
}

// checkStep checks one frame through c, and reports whether the shell's
// last frame has been seen.
func checkStep(t *testing.T, c *Checker, setup *terminalv1.Setup, frame *terminalv1.ServerFrame, ended bool) bool {
	t.Helper()
	out, fail := c.Check(frame)
	if ended && (out != nil || fail == nil) {
		t.Fatalf("after the last frame, Check = %v, %v", out, fail)
	}
	if fail != nil {
		checkFailure(t, fail)
		return true
	}
	if out != nil {
		checkOut(t, setup, out)
		return ended || out.GetExit() != nil
	}
	return ended
}

func checkFailure(t *testing.T, fail *Failure) {
	t.Helper()
	switch fail.Rule {
	case RuleFrame, RuleExitStatus, RuleFailed:
	default:
		t.Fatalf("failure rule %q", fail.Rule)
	}
	if len(fail.Message) > maxMessage || !utf8.ValidString(fail.Message) {
		t.Fatalf("failure message %q", fail.Message)
	}
	if guesttext.Strip(fail.Message) != fail.Message {
		t.Fatalf("failure message %q holds escaped bytes", fail.Message)
	}
}

func checkOut(t *testing.T, setup *terminalv1.Setup, out *terminalv1.ServerFrame) {
	t.Helper()
	switch fr := out.GetFrame().(type) {
	case *terminalv1.ServerFrame_Output:
		if len(fr.Output.GetData()) > MaxFrameData {
			t.Fatalf("output of %d bytes", len(fr.Output.GetData()))
		}
		s := fr.Output.GetStream()
		if s != terminalv1.Stream_STREAM_STDOUT && (s != terminalv1.Stream_STREAM_STDERR || !setup.GetSeparateStderr()) {
			t.Fatalf("output on stream %v", s)
		}
	case *terminalv1.ServerFrame_Stopped:
		if !setup.GetInteractive() {
			t.Fatal("stopped frame in non-interactive use")
		}
	case *terminalv1.ServerFrame_Exit:
		checkExitOut(t, fr.Exit)
	default:
		t.Fatalf("frame for wb %v", out)
	}
	b, err := proto.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var back terminalv1.ServerFrame
	if err := proto.Unmarshal(b, &back); err != nil || !proto.Equal(&back, out) {
		t.Fatalf("frame for wb doesn't round-trip: %v", err)
	}
	if len(out.ProtoReflect().GetUnknown()) != 0 {
		t.Fatal("frame for wb holds unknown fields")
	}
}

func checkExitOut(t *testing.T, e *terminalv1.Exit) {
	t.Helper()
	switch s := e.GetStatus().(type) {
	case *terminalv1.Exit_Code:
		if s.Code > maxExitCode {
			t.Fatalf("exit code %d", s.Code)
		}
	case *terminalv1.Exit_Signal:
		if s.Signal < 1 || s.Signal > maxSignal {
			t.Fatalf("signal %d", s.Signal)
		}
	default:
		t.Fatal("exit with no status")
	}
}

// splitSeq splits data into its length-prefixed parts. A part cut short
// by the end of data is kept as it is.
func splitSeq(data []byte) [][]byte {
	var parts [][]byte
	for len(data) >= 2 {
		n := int(binary.BigEndian.Uint16(data))
		data = data[2:]
		n = min(n, len(data))
		parts = append(parts, data[:n])
		data = data[n:]
	}
	return parts
}

func encodeSeq(tb testing.TB, frames []*terminalv1.ServerFrame) []byte {
	tb.Helper()
	var out []byte
	for _, fr := range frames {
		b, err := proto.Marshal(&guestv1.ShellResponse{Frame: fr})
		if err != nil {
			tb.Fatal(err)
		}
		out = binary.BigEndian.AppendUint16(out, uint16(len(b)))
		out = append(out, b...)
	}
	return out
}
