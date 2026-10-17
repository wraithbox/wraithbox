package termfilter

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// resetCase is one row of the removal tests: an input that changes a
// state, the reset step that undoes it, and the state that stays
// changed without that step.
type resetCase struct {
	step  string
	in    string
	state string
	// raw feeds in to the model without the filter. Only for a state
	// the filter keeps the guest from changing, whose step guards
	// against a later allowlist that passes it.
	raw bool
}

// removalCases hold at least one case for every step that resets a
// state (C10-terminal, "Fuzzing").
var removalCases = []resetCase{
	{"synchronized-output", "\x1b[?2026h", "modes", false},
	{"alternate-pops", "\x1b[?1049h\x1b[>1u", "kitty-alt", false},
	{"alternate-pops", "\x1b[?1049h\x1b[>1u\x1b[>3u\x1b[?1049l", "kitty-alt", false},
	{"alternate-screen", "\x1b[?1049h", "screen", false},
	{"main-pops", "\x1b[>1u", "kitty-main", false},
	{"main-pops", "\x1b[?1049h\x1b[>1u\x1b[?1049l\x1b[>1u\x1b[?1049h", "kitty-main", false},
	{"scroll-region", "\x1b[5;10r", "scroll-region", false},
	{"style", "\x1b[7m", "style", false},
	{"style", "\x1b[38;2;1;2;3m", "style", false},
	{"style", "\x1b[0m\x1b[1;2;3;4;5;7;8;9m\x1b[22;23;24;25;27;28;29m\x1b[7m", "style", false},
	{"mouse", "\x1b[?1000h", "modes", false},
	{"mouse", "\x1b[?1002h", "modes", false},
	{"mouse", "\x1b[?1003h", "modes", false},
	{"mouse", "\x1b[?1006h", "modes", false},
	{"mouse", "\x1b[?1016h", "modes", false},
	{"bracketed-paste", "\x1b[?2004h", "modes", false},
	{"focus-events", "\x1b[?1004h", "modes", false},
	{"color-scheme", "\x1b[?2031h", "modes", false},
	{"cursor-visible", "\x1b[?25l", "modes", false},
	{"cursor-style", "\x1b[4 q", "cursor-style", false},
	{"g0", "\x1b(0", "g0", false},
	{"g1", "\x1b)0", "g1", false},
	{"shift-in", "\x0e", "gl", true},
	{"modify-other-keys", "\x1b[>4;2m", "key-resources", false},
	{"keypad", "\x1b=", "keypad", false},
	{"tab-stops", "\x1b[3g", "tab-stops", false},
	{"progress", "\x1b]9;4;1;50\x07", "progress", false},
	{"status", "\x1b]7501;state=working\x1b\\", "status", false},
}

// replay filters in, with the BEL limit off, and replays the output
// and the reset without the step named skip into a new model. raw
// replays in unfiltered, and the reset of a filter that saw nothing.
func replay(t *testing.T, in []byte, skip string, raw bool) (*termModel, []byte) {
	t.Helper()
	var out bytes.Buffer
	f := New(&out, WithoutBELLimit())
	if !raw {
		if _, err := f.Write(in); err != nil {
			t.Fatalf("Write() = %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close() = %v", err)
		}
	} else {
		out.Write(in)
	}
	reset := f.exitReset(skip)
	m := newModel()
	m.feed(out.Bytes())
	m.feed(reset)
	return m, reset
}

func TestExitResetRestoresEveryState(t *testing.T) {
	for _, tt := range removalCases {
		t.Run(tt.step+"/"+fmt.Sprintf("%q", tt.in), func(t *testing.T) {
			m, reset := replay(t, []byte(tt.in), "", tt.raw)
			if c := m.changed(); len(c) > 0 {
				t.Fatalf("after the reset %q the model differs in %v", reset, c)
			}
		})
	}
}

// TestExitResetWithoutStep removes each step from the reset and sees
// the property fail in that step's state (C10-terminal, "Fuzzing").
func TestExitResetWithoutStep(t *testing.T) {
	for _, tt := range removalCases {
		t.Run(tt.step+"/"+fmt.Sprintf("%q", tt.in), func(t *testing.T) {
			m, reset := replay(t, []byte(tt.in), tt.step, tt.raw)
			init := initialState()
			if d := diff(&m.termState, &init); !slices.Contains(d, tt.state) {
				t.Fatalf("without %s, the reset %q leaves %v changed, want %s", tt.step, reset, d, tt.state)
			}
		})
	}
}

// TestRemovalCasesCoverSteps checks that every step but the new line
// has a removal case.
func TestRemovalCasesCoverSteps(t *testing.T) {
	for _, s := range resetSteps {
		if s.name == "new-line" {
			continue
		}
		if !slices.ContainsFunc(removalCases, func(c resetCase) bool { return c.step == s.name }) {
			t.Errorf("step %s has no removal case", s.name)
		}
	}
}

func TestExitResetEndsWithNewLine(t *testing.T) {
	f := New(io.Discard)
	if r := f.ExitReset(); !bytes.HasSuffix(r, []byte("\r\n")) {
		t.Fatalf("ExitReset() = %q, want it to end with CR LF", r)
	}
}

// resetInputs give the filter state that every step of the reset reads.
var resetInputs = []string{
	"",
	"\x1b[>1u\x1b[>1u\x1b[?1049h\x1b[>1u\x1b]9;4;1;50\x07\x1b]7501;state=working;title=x\x1b\\",
	"\x1b[?1049h\x1b[>1u\x1b[>1u\x1b[?1049l\x1b[>1u",
	"\x1b[>1u\x1b[>1u\x1b[>1u\x1b[>1u\x1b[>1u\x1b[<2u",
	"\x1b]7501;?\x07\x1b]9;4;x\x07\x1b]9;hello\x07",
}

// TestExitResetPassesTheFilter checks that the reset holds no `ESC c`,
// and that the filter passes every sequence of it unchanged, except
// `CSI ? 5 W` and the pops, which `wb` writes on purpose.
func TestExitResetPassesTheFilter(t *testing.T) {
	onPurpose := regexp.MustCompile(`^\x1b\[(?:\?5W|<[1-9][0-9]*u)$`)
	for _, in := range resetInputs {
		_, reset := replay(t, []byte(in), "", false)
		if bytes.Contains(reset, []byte("\x1bc")) {
			t.Fatalf("reset %q holds ESC c", reset)
		}
		var tok tokenizer
		var tokens []string
		tok.feed(reset, func(tk token) { tokens = append(tokens, tokenBytes(tk)) })
		if !tok.idle() {
			t.Fatalf("reset %q ends inside a sequence", reset)
		}
		for _, s := range tokens {
			if onPurpose.MatchString(s) {
				continue
			}
			got, d := filterString(t, s, WithoutBELLimit())
			if got != s || len(d) != 0 {
				t.Errorf("filter of reset token %q = %q, drops %v", s, got, d)
			}
		}
	}
}

// tokenBytes is a token with its terminator.
func tokenBytes(tk token) string {
	switch tk.term {
	case termBEL:
		return string(tk.raw) + "\x07"
	case termST, termC1ST:
		return string(tk.raw) + "\x1b\\"
	case termNone:
	}
	return string(tk.raw)
}

// TestExitResetSameState checks that the reset depends on the state
// only: two filters that saw the same input give the same bytes, and
// a second call gives the bytes of the first.
func TestExitResetSameState(t *testing.T) {
	for _, in := range resetInputs {
		a := New(io.Discard)
		b := New(io.Discard)
		_, _ = a.Write([]byte(in))
		for _, c := range chunksOf([]byte(in), 3) {
			_, _ = b.Write(c)
		}
		first := a.ExitReset()
		if again := a.ExitReset(); !bytes.Equal(first, again) {
			t.Errorf("second ExitReset() = %q, want %q", again, first)
		}
		if other := b.ExitReset(); !bytes.Equal(first, other) {
			t.Errorf("ExitReset() after split reads = %q, want %q", other, first)
		}
	}
}

// TestExitResetPops checks that the pops on each screen add up to the
// guest's pushes on it, so a pop never reaches the user's entry.
func TestExitResetPops(t *testing.T) {
	pop := regexp.MustCompile(`^\x1b\[<([0-9]+)u$`)
	for _, in := range resetInputs {
		f := New(io.Discard)
		_, _ = f.Write([]byte(in))
		var popped [2]int
		alt := f.kb.alt
		var tok tokenizer
		tok.feed(f.ExitReset(), func(tk token) {
			s := string(tk.raw)
			switch {
			case s == "\x1b[?1049h":
				alt = true
			case s == "\x1b[?1049l":
				alt = false
			case pop.MatchString(s):
				n, _ := number(pop.FindStringSubmatch(s)[1])
				popped[boolIndex(alt)] += n
			}
		})
		if popped != f.kb.pushes {
			t.Errorf("input %q: reset pops %v, guest pushed %v", in, popped, f.kb.pushes)
		}
	}
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestExitResetOnlyAfterReports checks that the progress and status
// clears run only when a report passed, and not for a query or a
// dropped report.
func TestExitResetOnlyAfterReports(t *testing.T) {
	tests := []struct {
		in             string
		progress, stat bool
	}{
		{"", false, false},
		{"\x1b]7501;?\x07\x1b]9;4;x\x07\x1b]9;hello\x07\x1b]7501;title=x\x07", false, false},
		{"\x1b]9;4;3\x1b\\", true, false},
		{"\x1b]7501;state=idle;msg=hi\x07", false, true},
	}
	for _, tt := range tests {
		f := New(io.Discard)
		_, _ = f.Write([]byte(tt.in))
		r := string(f.ExitReset())
		if got := strings.Contains(r, "\x1b]9;4;0\x1b\\"); got != tt.progress {
			t.Errorf("input %q: progress clear %v, want %v", tt.in, got, tt.progress)
		}
		if got := strings.Contains(r, "\x1b]7501;state=clear\x1b\\"); got != tt.stat {
			t.Errorf("input %q: status clear %v, want %v", tt.in, got, tt.stat)
		}
	}
}

// TestModel checks the model on known sequences, so a failure of the
// property points at the reset and not at the model.
func TestModel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string // the states that differ from the initial one
	}{
		{"text and moves", "abc\r\n\x1b[2J\x1b[H\x1b[5A\x1bM\x1b[6n", nil},
		{"partial resets", "\x1b[1;2;3;4;5;7;8;9m\x1b[22;23;24;25;27;28;29m", nil},
		{"inverse after reset", "\x1b[0m\x1b[7m", []string{"style"}},
		{"bold kept", "\x1b[1;7m\x1b[27m", []string{"style"}},
		{"colon colors", "\x1b[38:2::1:2:3m\x1b[48:5:3m\x1b[58:5:1m\x1b[39;49;59m", nil},
		{"semicolon colors", "\x1b[38;2;1;2;3;48;5;4m\x1b[39m", []string{"style"}},
		{"underline style", "\x1b[4:3m\x1b[24m\x1b[4:0m", nil},
		{"reset in the middle", "\x1b[1;0;7m", []string{"style"}},
		{"unnamed code", "\x1b[71m\x1b[22m", []string{"style"}},
		{"modes", "\x1b[?1000;1006h\x1b[?1000l", []string{"modes"}},
		{"cursor hidden and shown", "\x1b[?25l\x1b[?25h", nil},
		{"alt screen restores the cursor", "\x1b[1m\x1b[?1049h\x1b[0m\x1b(0\x1b[?1049l", []string{"style"}},
		{"alt stack kept", "\x1b[?1049h\x1b[>1u\x1b[?1049l", []string{"kitty-alt"}},
		{"pop past the guest", "\x1b[>1u\x1b[<2u", []string{"kitty-main"}},
		{"pop of own push", "\x1b[>1u\x1b[<u", nil},
		{"save and restore", "\x1b(0\x1b7\x1b(B\x1b8", []string{"g0"}},
		{"sco save", "\x1b[7m\x1b[s\x1b[0m\x1b[u", []string{"style"}},
		{"scroll region", "\x1b[5;10r\x1b[r\x1b[1;0r", nil},
		{"tab stops", "\x1b[3g\x1b[?5W\x1b[0g", []string{"tab-stops"}},
		{"keypad", "\x1b=\x1b>", nil},
		{"modifyOtherKeys", "\x1b[>4;2m\x1b[>4m\x1b[>1;1m", []string{"key-resources"}},
		{"soft reset", "\x1b[?25l\x1b[7m\x1b=\x1b(0\x1b[2;3r\x1b[!p", nil},
		{"soft reset keeps kitty", "\x1b[>1u\x1b[!p", []string{"kitty-main"}},
		{"cursor style", "\x1b[2 q\x1b[ q", nil},
		{"progress", "\x1b]9;4;1;50\x07\x1b]9;4;0\x1b\\", nil},
		{"status ids", "\x1b]7501;state=working;id=a\x1b\\\x1b]7501;state=clear;id=b\x1b\\", []string{"status"}},
		{"status clear all", "\x1b]7501;state=working;id=a\x1b\\\x1b]7501;state=blocked\x1b\\\x1b]7501;state=clear\x1b\\", nil},
		{"full reset unknown", "\x1bc", []string{"unknown"}},
		{"title unknown", "\x1b]0;x\x07", []string{"unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel()
			m.feed([]byte(tt.in))
			init := initialState()
			if got := diff(&m.termState, &init); !slices.Equal(got, tt.want) {
				t.Fatalf("model after %q differs in %v, want %v", tt.in, m.changed(), tt.want)
			}
		})
	}
}
