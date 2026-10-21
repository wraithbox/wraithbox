package relay

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/tty"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// refusedCharsets are host terminals the relay refuses
// (S16-01-filter, "Where"), with the text the refusal must hold.
var refusedCharsets = []struct {
	name string
	cs   tty.Charset
	want []string
}{
	{"none set", tty.Charset{}, []string{"none of LC_ALL, LC_CTYPE and LANG is set", "set LANG to a UTF-8 locale"}},
	{"C", tty.Charset{Source: "LANG", Value: "C"}, []string{`LANG is "C"`, "set LANG to a UTF-8 locale"}},
	{"POSIX in LC_CTYPE", tty.Charset{Source: "LC_CTYPE", Value: "POSIX"}, []string{`LC_CTYPE is "POSIX"`, "set LC_CTYPE to a UTF-8 locale"}},
	{"ISO8859-1 in LC_ALL", tty.Charset{Source: "LC_ALL", Value: "en_US.ISO8859-1"}, []string{`LC_ALL is "en_US.ISO8859-1"`, "set LC_ALL to a UTF-8 locale"}},
	{"a UTF-8 name not read as UTF-8", tty.Charset{Source: "LANG", Value: "en_US.UTF-8"}, []string{`LANG is "en_US.UTF-8"`}},
}

// TestCheckCharset: a charset that isn't UTF-8 is refused with a
// CharsetError that names what wb found and how to fix it, and the
// refusal is logged with its rule.
func TestCheckCharset(t *testing.T) {
	for _, tt := range refusedCharsets {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			err := CheckCharset(tt.cs, slog.New(slog.NewTextHandler(&logs, nil)))
			var ce *CharsetError
			if !errors.As(err, &ce) || ce.Charset != tt.cs {
				t.Fatalf("CheckCharset = %v, want a CharsetError for %+v", err, tt.cs)
			}
			checkHolds(t, err.Error(), append(tt.want, "isn't UTF-8"))
			if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "rule="+RuleCharset) {
				t.Errorf("log %q doesn't record the refusal with its rule", logs.String())
			}
		})
	}
	if err := CheckCharset(tty.Charset{}, nil); err == nil {
		t.Error("CheckCharset with no log accepted the zero Charset")
	}
}

// checkHolds fails for each of want that the refusal s doesn't hold.
func checkHolds(t *testing.T, s string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("refusal %q doesn't hold %q", s, w)
		}
	}
}

// TestCheckCharsetAccepts: a UTF-8 charset passes, and the decision is
// logged with its rule.
func TestCheckCharsetAccepts(t *testing.T) {
	var logs bytes.Buffer
	if err := CheckCharset(utf8Charset, slog.New(slog.NewTextHandler(&logs, nil))); err != nil {
		t.Errorf("CheckCharset(%+v) = %v, want nil", utf8Charset, err)
	}
	if !strings.Contains(logs.String(), "rule="+RuleCharset) || !strings.Contains(logs.String(), "value=en_US.UTF-8") {
		t.Errorf("log %q doesn't record the decision with its rule", logs.String())
	}
}

// TestRunRefusesCharset: Run refuses a host terminal that isn't UTF-8
// before it reads a frame. The guest's output, waiting on the stream,
// never reaches standard output or standard error, the terminal stays
// in its mode, no client frame is sent, the stream is closed, and the
// last line names the rule.
func TestRunRefusesCharset(t *testing.T) {
	for _, tt := range refusedCharsets {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, allTTYs)
			h.cfg.Charset = tt.cs
			h.stream.serve(stdout("guest\x1b]2;title\a"), outFrame(terminalv1.Stream_STREAM_STDERR, "guest error"), exitCode(0))
			h.start()
			if code := h.wait(t); code != ExitFailure {
				t.Errorf("exit status %d, want %d", code, ExitFailure)
			}
			checkNothingRelayed(t, h)
			checkRefusalLine(t, h)
		})
	}
}

// checkNothingRelayed fails when the relay read a server frame, sent a
// client frame, wrote to standard output, or set raw mode, or when it
// left the stream open.
func checkNothingRelayed(t *testing.T, h *harness) {
	t.Helper()
	if got := h.e.text("stdout"); got != "" {
		t.Errorf("standard output got %q, want nothing", got)
	}
	if h.e.index("raw", "") >= 0 {
		t.Error("the terminal was set to raw mode")
	}
	if len(h.stream.frames) != 3 {
		t.Errorf("%d of 3 server frames left on the stream, want none read", len(h.stream.frames))
	}
	h.stream.none(t, 0)
	select {
	case <-h.stream.closed:
	default:
		t.Error("the stream wasn't closed")
	}
}

// checkRefusalLine fails unless standard error holds only the
// `wraith box:` line of the charset refusal, and the log names its rule.
func checkRefusalLine(t *testing.T, h *harness) {
	t.Helper()
	stderr := h.e.text("stderr")
	if strings.Contains(stderr, "guest") || strings.Count(stderr, "\n") != 1 {
		t.Errorf("standard error %q, want the refusal line only", stderr)
	}
	want := (&CharsetError{Charset: h.cfg.Charset}).Line()
	if last := h.lastLine(); last != want || !strings.HasPrefix(last, "wraith box: the host terminal's charset isn't UTF-8") || !strings.HasSuffix(last, "(rule "+RuleCharset+")") {
		t.Errorf("last line %q, want %q", last, want)
	}
	if !strings.Contains(h.logs.String(), "rule="+RuleCharset) {
		t.Errorf("log %q doesn't name the rule", h.logs.String())
	}
}

// TestNewSetupRefusesCharset: the first message of a Shell call isn't
// built for a host terminal that isn't UTF-8, so the session is refused
// before wb-hostd opens it.
func TestNewSetupRefusesCharset(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	for _, tt := range refusedCharsets {
		s, lines, err := NewSetup(allTTYs, &fakeTerminal{rows: 24, cols: 80}, tt.cs, lookup, nil)
		var ce *CharsetError
		if !errors.As(err, &ce) || s != nil || lines != nil {
			t.Fatalf("%s: NewSetup = %v, %q, %v; want a CharsetError", tt.name, s, lines, err)
		}
		checkRefusalForCaller(t, ce)
	}
}

// checkRefusalForCaller: a caller of NewSetup gets the same `wraith
// box:` line with the rule, and the same exit status 255, as Run gives.
func checkRefusalForCaller(t *testing.T, ce *CharsetError) {
	t.Helper()
	want := "wraith box: " + ce.Error() + " (rule " + RuleCharset + ")"
	if ce.Line() != want {
		t.Errorf("Line = %q, want %q", ce.Line(), want)
	}
	if ce.ExitStatus() != ExitFailure {
		t.Errorf("ExitStatus = %d, want %d", ce.ExitStatus(), ExitFailure)
	}
}
