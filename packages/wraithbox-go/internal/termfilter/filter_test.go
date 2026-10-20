package termfilter

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// drops is the counts a test expects, without the rules at zero.
type drops map[Rule]uint64

// filterString filters in with one Write and a Close, and returns the
// output and the counts that aren't zero.
func filterString(t *testing.T, in string, opts ...Option) (string, drops) {
	t.Helper()
	var out bytes.Buffer
	f := New(&out, opts...)
	if n, err := f.Write([]byte(in)); err != nil || n != len(in) {
		t.Fatalf("Write() = %d, %v, want %d, nil", n, err, len(in))
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	return out.String(), nonZero(f.Counts())
}

func nonZero(c map[Rule]uint64) drops {
	d := drops{}
	for r, n := range c {
		if n > 0 {
			d[r] = n
		}
	}
	return d
}

type filterCase struct {
	name  string
	in    string
	want  string
	drops drops
}

func runCases(t *testing.T, tests []filterCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, d := filterString(t, tt.in, WithoutBELLimit())
			if got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
			want := tt.drops
			if want == nil {
				want = drops{}
			}
			if !maps.Equal(d, want) {
				t.Errorf("counts = %v, want %v", d, want)
			}
		})
	}
}

// TestAllowlistText covers the rows "Text" and "C0 controls, DEL" of
// S16-01-filter, "Allowlist".
func TestAllowlistText(t *testing.T) {
	runCases(t, []filterCase{
		{name: "utf-8 text", in: "hello é ✓ 😀 {\"a\": 1}", want: "hello é ✓ 😀 {\"a\": 1}"},
		{name: "c1 csi as a code point", in: "a\u009b31mb", want: "a31mb", drops: drops{RuleC1: 1}},
		{name: "c1 dcs osc apc pm sos", in: "\u0090\u009d\u009f\u009e\u0098", want: "", drops: drops{RuleC1: 5}},
		{name: "u+00a0 is text", in: "a\u00a0b", want: "a\u00a0b"},
		{name: "invalid byte", in: "a\xff" + "b", want: "a�b"},
		{name: "raw 8-bit csi", in: "\x9b31m", want: "�31m"},
		{name: "overlong", in: "\xc0\xaf" + "x", want: "��x"},
		{name: "overlong three bytes", in: "\xe0\x80\xaf" + "x", want: "���x"},
		{name: "surrogate", in: "\xed\xa0\x80", want: "���"},
		{name: "past u+10ffff", in: "\xf4\x90\x80\x80", want: "����"},
		{name: "truncated before ascii", in: "\xe2\x82A", want: "��A"},
		{name: "truncated before esc", in: "\xe2\x1b7", want: "�\x1b7"},
		{name: "c0 passed", in: "a\b\t\n\r\x0fb", want: "a\b\t\n\r\x0fb"},
		{name: "bel passed", in: "\a", want: "\a"},
		{name: "enq", in: "\x05", want: "", drops: drops{RuleC0: 1}},
		{name: "so, the neighbor of si", in: "\x0e", want: "", drops: drops{RuleC0: 1}},
		{name: "vt ff nul", in: "\v\f\x00", want: "", drops: drops{RuleC0: 3}},
		{name: "del", in: "a\x7fb", want: "ab", drops: drops{RuleC0: 1}},
		{name: "can and sub outside a sequence", in: "\x18\x1a", want: "", drops: drops{RuleC0: 2}},
	})
}

// TestAllowlistESC covers the row "ESC".
func TestAllowlistESC(t *testing.T) {
	passed := "\x1b7\x1b8\x1bM\x1bD\x1bE\x1b=\x1b>\x1b(B\x1b(0\x1b)B\x1b)0"
	runCases(t, []filterCase{
		{name: "every passed esc", in: passed, want: passed},
		{name: "esc c, full reset", in: "\x1bc", want: "", drops: drops{RuleESC: 1}},
		{name: "esc ( a, next to ( b", in: "\x1b(A", want: "", drops: drops{RuleESC: 1}},
		{name: "esc * 0, next to ) 0", in: "\x1b*0", want: "", drops: drops{RuleESC: 1}},
		{name: "screen alignment test", in: "\x1b#8", want: "", drops: drops{RuleESC: 1}},
		{name: "ss2", in: "\x1bN", want: "", drops: drops{RuleESC: 1}},
		{name: "stray st", in: "\x1b\\x", want: "x", drops: drops{RuleESC: 1}},
		{name: "esc esc", in: "\x1b\x1b7", want: "\x1b7", drops: drops{RuleESC: 1}},
		{name: "esc ended by a control", in: "\x1b(\nB", want: "\nB", drops: drops{RuleESC: 1}},
		{name: "esc ended by can", in: "\x1b(\x18B", want: "B", drops: drops{RuleESC: 1}},
		{name: "esc past its cap", in: "\x1b" + strings.Repeat("(", 20) + "Bx", want: "x", drops: drops{RuleESC: 1}},
	})
}

// csi64 is a CSI of exactly maxCSI bytes, 11 values, none over 5 digits.
var csi64 = "\x1b[" + strings.Repeat("12345;", 10) + "1m"

// TestAllowlistCSIParameters covers the row "CSI parameters".
func TestAllowlistCSIParameters(t *testing.T) {
	sixteen := "\x1b[" + strings.Repeat("1;", 15) + "1m"
	runCases(t, []filterCase{
		{name: "16 values", in: sixteen, want: sixteen},
		{name: "17 values", in: "\x1b[" + strings.Repeat("1;", 16) + "1m", want: "", drops: drops{RuleCSI: 1}},
		{name: "colon sub-parameters count as values", in: "\x1b[" + strings.Repeat("1:", 16) + "1m", want: "", drops: drops{RuleCSI: 1}},
		{name: "colon sub-parameters", in: "\x1b[38:2::1:2:3m", want: "\x1b[38:2::1:2:3m"},
		{name: "5 digits", in: "\x1b[12345C", want: "\x1b[12345C"},
		{name: "6 digits", in: "\x1b[123456C", want: "", drops: drops{RuleCSI: 1}},
		{name: "64 bytes", in: csi64, want: csi64},
		{name: "65 bytes", in: csi64[:len(csi64)-1] + "2mX", want: "X", drops: drops{RuleCSI: 1}},
		{name: "other parameter byte", in: "\x1b[1<2mX", want: "X", drops: drops{RuleCSI: 1}},
		{name: "intermediate before a parameter", in: "\x1b[ 1q", want: "", drops: drops{RuleCSI: 1}},
		{name: "prefix twice", in: "\x1b[??25h", want: "", drops: drops{RuleCSI: 1}},
	})
}

// TestAllowlistCSIScreen covers the row "CSI screen".
func TestAllowlistCSIScreen(t *testing.T) {
	var all strings.Builder
	for _, b := range "ABCDEFGHf`ade@LMPXbSTrIZgm" {
		all.WriteString("\x1b[2" + string(b))
	}
	runCases(t, []filterCase{
		{name: "every screen final", in: all.String(), want: all.String()},
		{name: "sgr truecolor", in: "\x1b[38;2;144;109;0mx\x1b[0m", want: "\x1b[38;2;144;109;0mx\x1b[0m"},
		{name: "j and k 0 to 2", in: "\x1b[J\x1b[0J\x1b[1J\x1b[2J\x1b[K\x1b[2K", want: "\x1b[J\x1b[0J\x1b[1J\x1b[2J\x1b[K\x1b[2K"},
		{name: "csi 3 j", in: "\x1b[3J", want: "", drops: drops{RuleCSI: 1}},
		{name: "csi 3 k", in: "\x1b[3K", want: "", drops: drops{RuleCSI: 1}},
		{name: "j with two values", in: "\x1b[1;2J", want: "", drops: drops{RuleCSI: 1}},
		{name: "selective erase", in: "\x1b[?2J", want: "", drops: drops{RuleCSI: 1}},
		{name: "scroll with one number", in: "\x1b[S\x1b[3S\x1b[T\x1b[3T", want: "\x1b[S\x1b[3S\x1b[T\x1b[3T"},
		{name: "xthimouse", in: "\x1b[1;1;1;1;24T", want: "", drops: drops{RuleCSI: 1}},
		{name: "scroll with two numbers", in: "\x1b[1;2S", want: "", drops: drops{RuleCSI: 1}},
		{name: "scroll with a sub-parameter", in: "\x1b[1:2T", want: "", drops: drops{RuleCSI: 1}},
		{name: "s and u", in: "\x1b[s\x1b[u", want: "\x1b[s\x1b[u"},
		{name: "s with a value", in: "\x1b[1;80s", want: "", drops: drops{RuleCSI: 1}},
		{name: "u with a value", in: "\x1b[1u", want: "", drops: drops{RuleCSI: 1}},
		{name: "unknown final", in: "\x1b[1y", want: "", drops: drops{RuleCSI: 1}},
		{name: "sgr with a prefix", in: "\x1b[?1m", want: "", drops: drops{RuleCSI: 1}},
	})
}

// TestAllowlistCSIModes covers the row "CSI modes".
func TestAllowlistCSIModes(t *testing.T) {
	var all strings.Builder
	for _, m := range []string{"25", "1000", "1002", "1003", "1004", "1006", "1016", "2004", "2026", "2031", "1049"} {
		all.WriteString("\x1b[?" + m + "h\x1b[?" + m + "l")
	}
	runCases(t, []filterCase{
		{name: "every mode", in: all.String(), want: all.String()},
		{name: "two modes", in: "\x1b[?25;2004h", want: "\x1b[?25;2004h"},
		{name: "csi ? 47 h", in: "\x1b[?47h", want: "", drops: drops{RuleCSI: 1}},
		{name: "mode 1", in: "\x1b[?1h", want: "", drops: drops{RuleCSI: 1}},
		{name: "one mode off the list", in: "\x1b[?25;9999h", want: "", drops: drops{RuleCSI: 1}},
		{name: "leading zero", in: "\x1b[?025h", want: "", drops: drops{RuleCSI: 1}},
		{name: "no mode", in: "\x1b[?h", want: "", drops: drops{RuleCSI: 1}},
		{name: "ansi mode", in: "\x1b[25h", want: "", drops: drops{RuleCSI: 1}},
		{name: "mode query", in: "\x1b[?2026$p\x1b[?1016$p", want: "\x1b[?2026$p\x1b[?1016$p"},
		{name: "mode query off the list", in: "\x1b[?47$p", want: "", drops: drops{RuleCSI: 1}},
		{name: "mode query of two", in: "\x1b[?25;2026$p", want: "", drops: drops{RuleCSI: 1}},
		{name: "ansi mode query", in: "\x1b[4$p", want: "", drops: drops{RuleCSI: 1}},
	})
}

// TestAllowlistCSIKeyboard covers the row "CSI keyboard". The stack
// depth is in TestKittyKeyboard.
func TestAllowlistCSIKeyboard(t *testing.T) {
	runCases(t, []filterCase{
		{name: "push pop query", in: "\x1b[>5u\x1b[?u\x1b[<u", want: "\x1b[>5u\x1b[?u\x1b[<u"},
		{name: "push without flags", in: "\x1b[>u\x1b[<1u", want: "\x1b[>u\x1b[<1u"},
		{name: "csi = 1 u", in: "\x1b[=1u", want: "", drops: drops{RuleCSI: 1}},
		{name: "push with two values", in: "\x1b[>1;2u", want: "", drops: drops{RuleCSI: 1}},
		{name: "query with a value", in: "\x1b[?1u", want: "", drops: drops{RuleCSI: 1}},
		{name: "modifyOtherKeys", in: "\x1b[>4;2m\x1b[>4m", want: "\x1b[>4;2m\x1b[>4m"},
		{name: "csi > 4 ; 1 m", in: "\x1b[>4;1m", want: "", drops: drops{RuleCSI: 1}},
		{name: "csi > 1 m", in: "\x1b[>1m", want: "", drops: drops{RuleCSI: 1}},
		{name: "cursor style", in: "\x1b[ q\x1b[2 q", want: "\x1b[ q\x1b[2 q"},
		{name: "decsca, next to sp q", in: "\x1b[1\"q", want: "", drops: drops{RuleCSI: 1}},
		{name: "soft reset", in: "\x1b[!p", want: "", drops: drops{RuleCSI: 1}},
		{name: "soft reset with a value", in: "\x1b[1!p", want: "", drops: drops{RuleCSI: 1}},
	})
}

// TestAllowlistCSIQueries covers the row "CSI queries", and that every
// other t counts as RuleCSIWindow.
func TestAllowlistCSIQueries(t *testing.T) {
	passed := "\x1b[c\x1b[0c\x1b[>c\x1b[>0c\x1b[5n\x1b[6n\x1b[>q\x1b[>0q\x1b[14t\x1b[16t\x1b[18t"
	runCases(t, []filterCase{
		{name: "every query", in: passed, want: passed},
		{name: "da1 with a value", in: "\x1b[1c", want: "", drops: drops{RuleCSI: 1}},
		{name: "extended cursor report", in: "\x1b[?6n", want: "", drops: drops{RuleCSI: 1}},
		{name: "xtversion with a value", in: "\x1b[>1q", want: "", drops: drops{RuleCSI: 1}},
		{name: "csi 21 t, title report", in: "\x1b[21t", want: "", drops: drops{RuleCSIWindow: 1}},
		{name: "csi 22 t, title stack", in: "\x1b[22t", want: "", drops: drops{RuleCSIWindow: 1}},
		{name: "csi 19 t, next to 18 t", in: "\x1b[19t", want: "", drops: drops{RuleCSIWindow: 1}},
		{name: "window move", in: "\x1b[3;0;0t", want: "", drops: drops{RuleCSIWindow: 1}},
		{name: "t over the digit cap", in: "\x1b[123456t", want: "", drops: drops{RuleCSIWindow: 1}},
		{name: "t over the value cap", in: "\x1b[" + strings.Repeat("1;", 16) + "1t", want: "", drops: drops{RuleCSIWindow: 1}},
		{name: "t with an intermediate", in: "\x1b[1 t", want: "", drops: drops{RuleCSIWindow: 1}},
	})
}

// TestAllowlistOSC covers the row "OSC".
func TestAllowlistOSC(t *testing.T) {
	runCases(t, []filterCase{
		{name: "progress", in: "\x1b]9;4;1;50\x07", want: "\x1b]9;4;1;50\x07"},
		{name: "progress as recorded", in: "\x1b]9;4;0;\x07", want: "\x1b]9;4;0;\x07"},
		{name: "progress with st", in: "\x1b]9;4;3\x1b\\", want: "\x1b]9;4;3\x1b\\"},
		{name: "progress with u+009c", in: "\x1b]9;4;3\u009cx", want: "\x1b]9;4;3\x1b\\x"},
		{name: "progress not numeric", in: "\x1b]9;4;x\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "progress with too many values", in: "\x1b]9;4;1;2;3;4\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "progress value too long", in: "\x1b]9;4;1000\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "color query", in: "\x1b]10;?\x07\x1b]11;?\x1b\\\x1b]12;?\x07", want: "\x1b]10;?\x07\x1b]11;?\x1b\\\x1b]12;?\x07"},
		{name: "color set", in: "\x1b]11;#ff0000\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "two color queries", in: "\x1b]10;?;?\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "color query with a leading zero", in: "\x1b]010;?\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "osc 13, next to 12", in: "\x1b]13;?\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "iTerm2 notification", in: "a\x1b]9;wb approve: allow evil.example?\x07b", want: "a\ab", drops: drops{RuleOSCNotify: 1}},
		{name: "notification starting with 4", in: "\x1b]9;4x\x07", want: "\a", drops: drops{RuleOSCNotify: 1}},
		{name: "kitty notification", in: "\x1b]99;i=1:d=0;hi\x1b\\", want: "\a", drops: drops{RuleOSCNotify: 1}},
		{name: "ghostty notification", in: "\x1b]777;notify;wb;approve evil.example\x07", want: "\a", drops: drops{RuleOSCNotify: 1}},
		{name: "titles", in: "\x1b]0;a\x07\x1b]1;b\x07\x1b]2;c\x1b\\", want: "", drops: drops{RuleOSCTitle: 3}},
		{name: "osc 8", in: "\x1b]8;;https://example.com\x07x\x1b]8;;\x07", want: "x", drops: drops{RuleOSCLink: 2}},
		{name: "osc 52 write", in: "\x1b]52;c;aGVsbG8=\x07", want: "", drops: drops{RuleOSCClipboard: 1}},
		{name: "osc 52 ; c ; ?", in: "\x1b]52;c;?\x07", want: "", drops: drops{RuleOSCClipboard: 1}},
		{name: "osc 052", in: "\x1b]052;c;?\x07", want: "", drops: drops{RuleOSCClipboard: 1}},
		{name: "osc 1337", in: "\x1b]1337;File=name=eA==;size=1:eA==\x07", want: "", drops: drops{RuleOSCFile: 1}},
		{name: "osc 7", in: "\x1b]7;file://host/etc\x07", want: "", drops: drops{RuleOSCFile: 1}},
		{name: "osc 5113", in: "\x1b]5113;ac=send;id=1\x1b\\", want: "", drops: drops{RuleOSCFile: 1}},
		{name: "osc 133", in: "\x1b]133;A;redraw=0\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "osc 4", in: "\x1b]4;1;rgb:0/0/0\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "osc without a number", in: "\x1b];x\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "osc with a word for a number", in: "\x1b]x52;c;?\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "osc with a huge number", in: "\x1b]1234567890;?\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "osc 52 with many leading zeros", in: "\x1b]" + strings.Repeat("0", 50) + "52;c;?\x07", want: "", drops: drops{RuleOSCClipboard: 1}},
		{name: "control inside an osc", in: "\x1b]0;a\nb\x07c", want: "c", drops: drops{RuleOSCTitle: 1}},
	})
}

// TestAllowlistStrings covers the row "DCS, APC, PM, SOS".
func TestAllowlistStrings(t *testing.T) {
	runCases(t, []filterCase{
		{name: "decrqss", in: "\x1bP$qm\x1b\\x", want: "x", drops: drops{RuleDCS: 1}},
		{name: "xtgettcap", in: "\x1bP+q544e\x1b\\", want: "", drops: drops{RuleDCS: 1}},
		{name: "kitty graphics", in: "\x1b_Ga=T,t=f;L2V0Yy9ob3N0cw==\x1b\\x", want: "x", drops: drops{RuleAPC: 1}},
		{name: "pm", in: "\x1b^" + "secret\x1b\\", want: "", drops: drops{RulePMOrSOS: 1}},
		{name: "sos", in: "\x1bX" + "secret\x1b\\", want: "", drops: drops{RulePMOrSOS: 1}},
		{name: "bel doesn't end a dcs", in: "\x1bPa\x07b\x1b\\x", want: "x", drops: drops{RuleDCS: 1}},
		{name: "dcs ended by u+009c", in: "\x1bPa\u009cx", want: "x", drops: drops{RuleDCS: 1}},
		{name: "0xc2 inside a dcs", in: "\x1bPa\xc2\xc2\x9cx", want: "x", drops: drops{RuleDCS: 1}},
		{
			// The DCS ends at the inner ESC, which is not ST. The OSC 52
			// is dropped, and so is the stray ST.
			name:  "tmux pass-through",
			in:    "tmux:\x1bP" + "tmux;\x1b\x1b]52;c;eA==\x07\x1b\\:end",
			want:  "tmux::end",
			drops: drops{RuleDCS: 1, RuleESC: 2, RuleOSCClipboard: 1},
		},
	})
}

// TestWhichRule covers S16-02-drops-and-links, "Which rule a drop
// counts under".
func TestWhichRule(t *testing.T) {
	big := strings.Repeat("A", maxString+100)
	runCases(t, []filterCase{
		{name: "csi ended by can", in: "\x1b[1\x18X", want: "X", drops: drops{RuleCSI: 1}},
		{name: "csi ended by sub", in: "\x1b[1\x1aX", want: "X", drops: drops{RuleCSI: 1}},
		{name: "csi ended by esc", in: "\x1b[1\x1b[mX", want: "\x1b[mX", drops: drops{RuleCSI: 1}},
		{name: "csi t ended by can", in: "\x1b[21\x18", want: "", drops: drops{RuleCSI: 1}},
		{name: "csi ended by lf", in: "\x1b[1\n2mX", want: "\n2mX", drops: drops{RuleCSI: 1}},
		{name: "csi ended by del", in: "\x1b[1\x7fm", want: "m", drops: drops{RuleCSI: 1, RuleC0: 1}},
		{name: "csi ended by utf-8", in: "\x1b[1é", want: "é", drops: drops{RuleCSI: 1}},
		{name: "csi ended by u+009c", in: "\x1b[1\u009c", want: "", drops: drops{RuleCSI: 1, RuleC1: 1}},
		{name: "osc 52 cut at 16 KB", in: "\x1b]52;c;" + big + "\x07X", want: "X", drops: drops{RuleOSCClipboard: 1}},
		{name: "osc 52 cut at 16 KB, st", in: "\x1b]52;c;" + big + "\x1b\\X", want: "X", drops: drops{RuleOSCClipboard: 1}},
		{name: "title cut at 16 KB, then esc", in: "\x1b]0;" + big + "\x1b[1mX", want: "\x1b[1mX", drops: drops{RuleOSCTitle: 1}},
		{name: "osc 52 ended by can", in: "\x1b]52;c;eA==\x18X", want: "X", drops: drops{RuleOSCClipboard: 1}},
		{name: "osc 52 ended by esc", in: "\x1b]52;c;eA==\x1b[1mX", want: "\x1b[1mX", drops: drops{RuleOSCClipboard: 1}},
		{name: "notification ended by can", in: "\x1b]9;hi\x18X", want: "X", drops: drops{RuleOSC: 1}},
		{name: "color query ended by esc", in: "\x1b]11;?\x1b7", want: "\x1b7", drops: drops{RuleOSC: 1}},
		{name: "osc cut before its number ends", in: "\x1b]5\x18", want: "", drops: drops{RuleOSC: 1}},
		{name: "apc ended by sub", in: "\x1b_G\x1aX", want: "X", drops: drops{RuleAPC: 1}},
	})
}

// TestAPCOfOneMB writes an APC of 1 MB in reads of 32 KB, and sees one
// drop and a buffer that stays at its cap (SEC13-bounded-resources).
func TestAPCOfOneMB(t *testing.T) {
	var out bytes.Buffer
	f := New(&out)
	write := func(s string) {
		t.Helper()
		if _, err := f.Write([]byte(s)); err != nil {
			t.Fatalf("Write() = %v", err)
		}
	}
	write("a\x1b_Ga=T,t=f;")
	chunk := strings.Repeat("abcd", 8*1024)
	for range 32 {
		write(chunk)
		if len(f.tok.seq) > maxString || cap(f.tok.seq) > 2*maxString {
			t.Fatalf("buffer len %d cap %d, want at most %d", len(f.tok.seq), cap(f.tok.seq), maxString)
		}
	}
	write("\x1b\\b")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ab" {
		t.Errorf("output = %q, want %q", out.String(), "ab")
	}
	if d := nonZero(f.Counts()); !maps.Equal(d, drops{RuleAPC: 1}) {
		t.Errorf("counts = %v, want one %s", d, RuleAPC)
	}
}

// TestKittyKeyboard covers S16-01-filter, "Kitty keyboard stacks".
func TestKittyKeyboard(t *testing.T) {
	push, pop := "\x1b[>1u", "\x1b[<u"
	four := strings.Repeat(push, 4)
	runCases(t, []filterCase{
		{name: "five pushes", in: four + push, want: four, drops: drops{RuleCSI: 1}},
		{name: "pop past the pushes", in: push + pop + pop, want: push + pop, drops: drops{RuleCSI: 1}},
		{name: "pop of two after one push", in: push + "\x1b[<2u", want: push, drops: drops{RuleCSI: 1}},
		{name: "pop of 0", in: push + "\x1b[<0u", want: push, drops: drops{RuleCSI: 1}},
		{name: "pop with a leading zero", in: push + "\x1b[<01u", want: push, drops: drops{RuleCSI: 1}},
		{name: "pop with nothing pushed", in: pop, want: "", drops: drops{RuleCSI: 1}},
		{
			name:  "screens counted apart",
			in:    four + "\x1b[?1049h" + four + push + "\x1b[?1049l" + push + "\x1b[<4u" + pop,
			want:  four + "\x1b[?1049h" + four + "\x1b[?1049l" + "\x1b[<4u",
			drops: drops{RuleCSI: 3},
		},
		{
			name:  "pops on the other screen",
			in:    push + "\x1b[?25;1049h" + pop + "\x1b[?1049l" + pop,
			want:  push + "\x1b[?25;1049h" + "\x1b[?1049l" + pop,
			drops: drops{RuleCSI: 1},
		},
		{
			name:  "a dropped switch doesn't switch",
			in:    four + "\x1b[?1049;47h" + push,
			want:  four,
			drops: drops{RuleCSI: 2},
		},
	})
}

// TestProgramStatus covers S16-01-filter, "Program status".
func TestProgramStatus(t *testing.T) {
	long := "\x1b]7501;state=idle;title=" + strings.Repeat("x", maxStatusBytes) + "\x07"
	runCases(t, []filterCase{
		{name: "query", in: "\x1b]7501;?\x07", want: "\x1b]7501;?\x07"},
		{name: "query with st", in: "\x1b]7501;?\x1b\\", want: "\x1b]7501;?\x1b\\"},
		{name: "query with u+009c", in: "\x1b]7501;?\u009c", want: "\x1b]7501;?\x1b\\"},
		{name: "report as is", in: "\x1b]7501;state=working\x1b\\", want: "\x1b]7501;state=working\x1b\\"},
		{name: "report with bel ends with st", in: "\x1b]7501;state=working\x07", want: "\x1b]7501;state=working\x1b\\"},
		{
			name:  "title and msg removed",
			in:    "\x1b]7501;state=blocked;title=Approve?;msg=allow evil.example, press y;id=a/b;app=claude\x07",
			want:  "\x1b]7501;state=blocked;id=a/b;app=claude\x1b\\",
			drops: drops{RuleOSCStatusText: 1},
		},
		{
			name:  "other keys removed",
			in:    "\x1b]7501;state=done;color=red\x07",
			want:  "\x1b]7501;state=done\x1b\\",
			drops: drops{RuleOSCStatusText: 1},
		},
		{
			name: "every kept key, in the table's order",
			in:   "\x1b]7501;app=claude;progress=100;kind=permission;id=s.1/t-2/u+3/v_4;state=blocked\x07",
			want: "\x1b]7501;state=blocked;id=s.1/t-2/u+3/v_4;kind=permission;progress=100;app=claude\x1b\\",
		},
		{name: "progress 0", in: "\x1b]7501;state=working;progress=0\x07", want: "\x1b]7501;state=working;progress=0\x1b\\"},
		{name: "state missing", in: "\x1b]7501;id=a;title=x\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "state unknown", in: "\x1b]7501;state=approve\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "state repeated", in: "\x1b]7501;state=idle;state=done\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "removed key repeated", in: "\x1b]7501;state=idle;msg=a;msg=b\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "id with an empty segment", in: "\x1b]7501;state=idle;id=a//b\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "id with a space", in: "\x1b]7501;state=idle;id=a b\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "id segment of 33", in: "\x1b]7501;state=idle;id=" + strings.Repeat("a", 33) + "\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "id of 9 segments", in: "\x1b]7501;state=idle;id=" + strings.Repeat("a/", 8) + "a\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "id over 128 bytes", in: "\x1b]7501;state=idle;id=" + strings.Repeat(strings.Repeat("a", 32)+"/", 4) + "a\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "progress=101", in: "\x1b]7501;state=working;progress=101\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "progress=050", in: "\x1b]7501;state=working;progress=050\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "kind unknown", in: "\x1b]7501;state=blocked;kind=approval\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "app of 33", in: "\x1b]7501;state=idle;app=" + strings.Repeat("a", 33) + "\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "empty value", in: "\x1b]7501;state=idle;title=\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "value with =", in: "\x1b]7501;state=idle;msg=a=b\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "pair without =", in: "\x1b]7501;state=idle;msg\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "empty key", in: "\x1b]7501;state=idle;=x\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "empty pair", in: "\x1b]7501;state=idle;\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "no payload", in: "\x1b]7501\x07", want: "", drops: drops{RuleOSC: 1}},
		{name: "over 4096 bytes", in: long, want: "", drops: drops{RuleOSC: 1}},
		{name: "number with a leading zero", in: "\x1b]07501;?\x07", want: "", drops: drops{RuleOSC: 1}},
	})
}

// TestStatusAt4096 checks the edge of the 4096-byte limit, terminator
// included.
func TestStatusAt4096(t *testing.T) {
	head := "\x1b]7501;state=idle;title="
	fill := strings.Repeat("x", maxStatusBytes-len(head)-1)
	got, d := filterString(t, head+fill+"\x07")
	if got != "\x1b]7501;state=idle\x1b\\" || !maps.Equal(d, drops{RuleOSCStatusText: 1}) {
		t.Errorf("4096 bytes: output %q, counts %v", got, d)
	}
	got, d = filterString(t, head+fill+"\x1b\\")
	if got != "" || !maps.Equal(d, drops{RuleOSC: 1}) {
		t.Errorf("4097 bytes: output %q, counts %v", got, d)
	}
}

// fakeClock is a clock a test moves by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

// TestBELLimit covers the BEL rate limit of S16-01-filter, "Allowlist",
// with notifications going through the same limit.
func TestBELLimit(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}
	var out bytes.Buffer
	f := New(&out, WithClock(clock.now))
	steps := []struct {
		after time.Duration
		in    string
		want  string
	}{
		{0, "\a", "\a"},
		{0, "\a\a", ""},
		{500 * time.Millisecond, "\a", ""},
		{500 * time.Millisecond, "\a", "\a"},
		{200 * time.Millisecond, "\x1b]9;approve?\x07", ""},
		{time.Second, "\x1b]777;notify;a;b\x07\a", "\a"},
		{time.Second, "\a\x1b]99;;x\x1b\\", "\a"},
		{-time.Hour, "\a", ""},
	}
	for i, s := range steps {
		clock.add(s.after)
		out.Reset()
		if _, err := f.Write([]byte(s.in)); err != nil {
			t.Fatal(err)
		}
		if out.String() != s.want {
			t.Errorf("step %d: output %q, want %q", i, out.String(), s.want)
		}
	}
	want := drops{RuleC0: 7, RuleOSCNotify: 3}
	if d := nonZero(f.Counts()); !maps.Equal(d, want) {
		t.Errorf("counts = %v, want %v", d, want)
	}
}

// TestTerminalAppProfile checks that the sequences X19-terminal-filter
// recorded for the Terminal.app profile pass byte for byte, written by
// hand. The recording also held OSC 0 titles, which S16-01-filter drops
// since `wb` owns the title (S16-03-around-the-stream).
func TestTerminalAppProfile(t *testing.T) {
	in := strings.Join([]string{
		"\x1b[?2004h\x1b[?1004h\x1b[?25l\x1b[?2031h\x1b[?1016$p\x1b[c\x1b[>0q\x1b[16t\x1b[?u",
		"\x1b[>4;2m\x1b7\x1b[r\x1b8\x1b(B\x0f",
		"\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?1016l",
		"\x1b[2K\x1b[1A\x1b[2K\x1b[G\x1b[38;2;215;119;87m✻\x1b[39m \x1b[1mWelcome\x1b[22m\r\n",
		"\x1b[4A\x1b[4B\x1b[1C\x1b[1D\x1b[2G\x1b[H\x1b[2J\x1b[2S\x1b[3T",
		"│ \x1b[38;2;144;109;0m" + "diff\x1b[0m │\r\n",
		"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1004l\x1b[?2004l\x1b[?2031l\x1b[?25h\x1b[>4m",
	}, "")
	got, d := filterString(t, in)
	if got != in {
		t.Errorf("output differs:\n got %q\nwant %q", got, in)
	}
	if len(d) != 0 {
		t.Errorf("counts = %v, want none", d)
	}
	if _, d := filterString(t, "\x1b]0;✳ Claude Code\x07"); !maps.Equal(d, drops{RuleOSCTitle: 1}) {
		t.Errorf("title counts = %v", d)
	}
}

// TestWriteHoldsOnlyTheUnfinishedToken checks that text in front of a
// split character or sequence isn't delayed.
func TestWriteHoldsOnlyTheUnfinishedToken(t *testing.T) {
	var out bytes.Buffer
	f := New(&out)
	steps := []struct{ in, want string }{
		{"ab\xe2\x82", "ab"},
		{"\xac", "€"},
		{"x\x1b[3", "x"},
		{"1m", "\x1b[31m"},
		{"y\x1b]52;c;", "y"},
		{"eA==\x07z", "z"},
		{"\x1b]9;4;1\xc2", ""},
		{"\x9c", "\x1b]9;4;1\x1b\\"},
	}
	for i, s := range steps {
		out.Reset()
		if _, err := f.Write([]byte(s.in)); err != nil {
			t.Fatal(err)
		}
		if out.String() != s.want {
			t.Errorf("step %d: output %q, want %q", i, out.String(), s.want)
		}
	}
}

// TestClose checks what Close does with what the filter holds.
func TestClose(t *testing.T) {
	runCases(t, []filterCase{
		{name: "partial character", in: "a\xe2\x82", want: "a��"},
		{name: "partial osc 52", in: "a\x1b]52;c;eA", want: "a", drops: drops{RuleOSCClipboard: 1}},
		{name: "osc 52 at its esc", in: "\x1b]52;c;eA\x1b", want: "", drops: drops{RuleOSCClipboard: 1}},
		{name: "progress at 0xc2", in: "\x1b]9;4;1\xc2", want: "", drops: drops{RuleOSC: 1}},
		{name: "partial csi t", in: "\x1b[21", want: "", drops: drops{RuleCSI: 1}},
		{name: "lone esc", in: "\x1b", want: "", drops: drops{RuleESC: 1}},
		{name: "partial apc", in: "\x1b_G", want: "", drops: drops{RuleAPC: 1}},
		{name: "csi past its cap", in: "\x1b[" + strings.Repeat("1", 100), want: "", drops: drops{RuleCSI: 1}},
	})
}

func TestWriteAfterClose(t *testing.T) {
	f := New(io.Discard)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close() = %v", err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Errorf("Write() after Close = %v, want ErrClosed", err)
	}
}

type failWriter struct {
	n   int
	err error
}

func (w failWriter) Write(p []byte) (int, error) {
	return min(w.n, len(p)), w.err
}

func TestWriterErrors(t *testing.T) {
	boom := errors.New("boom")
	f := New(failWriter{n: 0, err: boom})
	if n, err := f.Write([]byte("abc")); !errors.Is(err, boom) || n != 3 {
		t.Errorf("Write() = %d, %v, want 3, boom", n, err)
	}
	if _, err := f.Write([]byte("abc")); !errors.Is(err, boom) {
		t.Errorf("second Write() = %v, want boom again", err)
	}
	if err := f.Close(); !errors.Is(err, boom) {
		t.Errorf("Close() = %v, want boom", err)
	}

	short := New(failWriter{n: 1})
	if _, err := short.Write([]byte("abc")); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("short Write() = %v, want io.ErrShortWrite", err)
	}

	dropped := New(failWriter{n: 0, err: boom})
	if n, err := dropped.Write([]byte("\x1bc")); err != nil || n != 2 {
		t.Errorf("Write() of a dropped sequence = %d, %v, want 2, nil", n, err)
	}
}

func TestRules(t *testing.T) {
	want := []Rule{
		"terminal-c1", "terminal-c0", "terminal-esc", "terminal-csi", "terminal-csi-window",
		"terminal-osc-title", "terminal-osc-link", "terminal-osc-clipboard", "terminal-osc-file",
		"terminal-osc", "terminal-osc-notify", "terminal-osc-status-text",
		"terminal-dcs", "terminal-apc", "terminal-pm-sos",
	}
	got := Rules()
	if !slices.Equal(got, want) {
		t.Fatalf("Rules() = %v, want %v", got, want)
	}
	got[0] = "terminal-made-up"
	if Rules()[0] != RuleC1 {
		t.Error("Rules() returns its own list")
	}
	for _, r := range want {
		if !r.Valid() {
			t.Errorf("%s.Valid() = false", r)
		}
	}
	if Rule("terminal-made-up").Valid() {
		t.Error("a made-up rule is valid")
	}
}

func TestCountsIsACopy(t *testing.T) {
	f := New(io.Discard)
	if _, err := f.Write([]byte("\x1bc")); err != nil {
		t.Fatal(err)
	}
	c := f.Counts()
	if len(c) != len(Rules()) {
		t.Errorf("Counts() has %d rules, want %d", len(c), len(Rules()))
	}
	c[RuleESC] = 0
	c["terminal-made-up"] = 1
	if got := f.Counts(); got[RuleESC] != 1 || len(got) != len(Rules()) {
		t.Errorf("Counts() after changing a copy = %v", got)
	}
}
