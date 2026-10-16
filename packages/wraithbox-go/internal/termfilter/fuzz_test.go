package termfilter

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// The property checker of C10-terminal, "Fuzzing". It checks output
// tokens against its own patterns, written from the table in
// S16-01-filter, "Allowlist", and not with the filter's decision code,
// so a bug there isn't mirrored here. It reuses the tokenizer.

// modeAlt lists the modes of the row "CSI modes".
const modeAlt = "(?:25|1000|1002|1003|1004|1006|1016|1049|2004|2026|2031)"

// allowedCSI matches every CSI the allowlist passes, before the caps.
var allowedCSI = regexp.MustCompile("^\x1b\\[(?:" + strings.Join([]string{
	"[0-9;:]*[ABCDEFGHf`ade@LMPXbrIZgm]", // screen
	"[0-9]*[ST]",                         // scroll, one number at most
	"0*[0-2]?[JK]",                       // erase 0 to 2
	"[su]",                               // save and restore without parameters
	"\\?" + modeAlt + "(?:;" + modeAlt + ")*[hl]", // modes
	"\\?" + modeAlt + "\\$p",                      // mode query
	">[0-9]*u", "<(?:[1-9][0-9]*)?u", "\\?u",      // kitty keyboard
	">4(?:;2)?m",       // modifyOtherKeys
	"[0-9;:]* q", "!p", // cursor style, soft reset
	"0?c", ">0?c", "[56]n", ">0?q", "1[468]t", // queries
}, "|") + ")$")

// statusID is the form of an OSC 7501 id.
const statusIDForm = "[A-Za-z0-9_.+-]{1,32}(?:/[A-Za-z0-9_.+-]{1,32}){0,7}"

// allowedOSC matches every OSC the allowlist passes, terminator
// included. A rebuilt OSC 7501 report lists its keys in the table's
// order and ends with ST.
var allowedOSC = regexp.MustCompile("^\x1b\\](?:" + strings.Join([]string{
	"9;4(?:;[0-9]{0,3}){0,3}(?:\x07|\x1b\\\\)",
	"1[0-2];\\?(?:\x07|\x1b\\\\)",
	"7501;\\?(?:\x07|\x1b\\\\)",
	"7501;state=(?:idle|working|done|blocked|error|clear)" +
		"(?:;id=" + statusIDForm + ")?" +
		"(?:;kind=(?:permission|question|auth))?" +
		"(?:;progress=(?:100|[1-9]?[0-9]))?" +
		"(?:;app=[A-Za-z0-9_.+-]{1,32})?\x1b\\\\",
}, "|") + ")$")

var allowedESCs = []string{"7", "8", "M", "D", "E", "=", ">", "(B", "(0", ")B", ")0"}

// stringIntroducers are DCS, SOS, PM and APC, 7-bit. Their C1 forms are
// C1 code points, which checkOutput refuses anywhere.
var stringIntroducers = []string{"\x1bP", "\x1bX", "\x1b^", "\x1b_"}

// checker keeps what the property checker needs across tokens.
type checker struct {
	err    error
	alt    bool
	pushes [2]int
}

// checkOutput checks the C10-terminal properties of one output that
// hold for the output alone, and returns the first one it breaks.
func checkOutput(out []byte) error {
	if !utf8.Valid(out) {
		return fmt.Errorf("output isn't valid UTF-8: %q", out)
	}
	for _, r := range string(out) {
		if r >= 0x80 && r <= 0x9f {
			return fmt.Errorf("output holds C1 code point %U: %q", r, out)
		}
	}
	for _, s := range stringIntroducers {
		if bytes.Contains(out, []byte(s)) {
			return fmt.Errorf("output holds introducer %q: %q", s, out)
		}
	}
	c := &checker{}
	var tok tokenizer
	tok.feed(out, c.token)
	if c.err != nil {
		return c.err
	}
	if !tok.idle() {
		return fmt.Errorf("output ends inside a sequence: %q", out)
	}
	return nil
}

func (c *checker) token(tk token) {
	if c.err != nil {
		return
	}
	ok := false
	switch tk.kind {
	case kText:
		ok = !bytes.ContainsFunc(tk.raw, func(r rune) bool { return r < 0x20 || r == 0x7f })
	case kControl:
		ok = strings.IndexByte("\a\b\t\n\r\x0f", tk.raw[0]) >= 0
	case kESC:
		ok = !tk.cut && slices.Contains(allowedESCs, string(tk.raw[1:]))
	case kCSI:
		ok = !tk.cut && c.csi(string(tk.raw))
	case kOSC:
		ok = !tk.cut && c.osc(tk)
	case kInvalid, kC1, kDCS, kSOS, kPM, kAPC:
	}
	if !ok {
		c.err = fmt.Errorf("output token %q (kind %d, cut %v) isn't on the allowlist", tk.raw, tk.kind, tk.cut)
	}
}

// csi checks a CSI against the patterns, the caps and the kitty
// keyboard stacks.
func (c *checker) csi(s string) bool {
	if len(s) > 64 || !allowedCSI.MatchString(s) {
		return false
	}
	values := strings.FieldsFunc(s[2:len(s)-1], func(r rune) bool { return r == ';' || r == ':' })
	if strings.Count(s, ";")+strings.Count(s, ":") >= 16 {
		return false
	}
	for _, v := range values {
		if len(strings.Trim(v, "<=>? !$")) > 5 {
			return false
		}
	}
	return c.kitty(s)
}

// kitty follows the screens and the guest's pushes, and refuses a push
// past 4 or a pop past the pushes on the current screen.
func (c *checker) kitty(s string) bool {
	screen := 0
	if c.alt {
		screen = 1
	}
	switch {
	case strings.HasPrefix(s, "\x1b[?") && strings.HasSuffix(s, "h") && strings.Contains(s, "1049"):
		c.alt = true
	case strings.HasPrefix(s, "\x1b[?") && strings.HasSuffix(s, "l") && strings.Contains(s, "1049"):
		c.alt = false
	case strings.HasPrefix(s, "\x1b[>") && strings.HasSuffix(s, "u"):
		c.pushes[screen]++
	case strings.HasPrefix(s, "\x1b[<") && strings.HasSuffix(s, "u"):
		n := 1
		if d := s[3 : len(s)-1]; d != "" {
			n = 0
			for _, r := range d {
				n = n*10 + int(r-'0')
			}
		}
		c.pushes[screen] -= n
	}
	return c.pushes[screen] >= 0 && c.pushes[screen] <= 4
}

// osc checks an OSC with its terminator, and that none of OSC 0 to 2,
// 8, 52 or 1337 is in the output.
func (c *checker) osc(tk token) bool {
	s := string(tk.raw)
	switch tk.term {
	case termBEL:
		s += "\x07"
	case termST:
		s += "\x1b\\"
	case termC1ST, termNone:
		return false
	}
	num, _, _ := strings.Cut(s[2:], ";")
	sig := strings.TrimLeft(num, "0")
	if sig == "" && num != "" {
		sig = "0"
	}
	if slices.Contains([]string{"0", "1", "2", "8", "52", "1337"}, sig) {
		return false
	}
	if strings.HasPrefix(s, "\x1b]7501;") && !statusWithinCaps(s) {
		return false
	}
	return allowedOSC.MatchString(s)
}

// statusWithinCaps checks the length caps of an OSC 7501 report that
// the pattern doesn't: 4096 bytes in all, and 128 bytes of id.
func statusWithinCaps(s string) bool {
	if len(s) > 4096 {
		return false
	}
	for pair := range strings.SplitSeq(s, ";") {
		if id, ok := strings.CutPrefix(pair, "id="); ok && len(id) > 128 {
			return false
		}
	}
	return true
}

// filterChunks filters the chunks with one Write each and a Close, with
// the BEL limit off, and checks that no count goes down between writes.
func filterChunks(t *testing.T, chunks ...[]byte) ([]byte, map[Rule]uint64) {
	t.Helper()
	var out bytes.Buffer
	f := New(&out, WithoutBELLimit())
	prev := f.Counts()
	step := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("filter error: %v", err)
		}
		now := f.Counts()
		for r, n := range now {
			if n < prev[r] {
				t.Fatalf("count %s went down from %d to %d", r, prev[r], n)
			}
		}
		prev = now
	}
	for _, c := range chunks {
		_, err := f.Write(c)
		step(err)
	}
	step(f.Close())
	return out.Bytes(), f.Counts()
}

// chunksOf splits p into chunks of n bytes.
func chunksOf(p []byte, n int) [][]byte {
	var cs [][]byte
	for len(p) > n {
		cs = append(cs, p[:n])
		p = p[n:]
	}
	return append(cs, p)
}

// fuzzSeeds hold each row of the allowlist, OSC 7501 reports, and the
// sequences of the X19-terminal-filter hostile script.
var fuzzSeeds = []string{
	"hello é ✓ \u009b31m \x9b31m \xe2\x82",
	"\a\b\t\n\r\x0f\x05\x0e\x7f\x00",
	"\x1b7\x1b8\x1bM\x1bD\x1bE\x1b=\x1b>\x1b(B\x1b(0\x1b)B\x1b)0\x1bc\x1b(A",
	"\x1b[" + strings.Repeat("1;", 15) + "1m\x1b[123456C\x1b[1<2m",
	"\x1b[38;2;144;109;0mx\x1b[0m\x1b[2J\x1b[3J\x1b[K\x1b[s\x1b[u\x1b[2S",
	"\x1b[?2026h\x1b[?1049h\x1b[?47h\x1b[?2026$p\x1b[?47$p\x1b[?1049l",
	"\x1b[>5u\x1b[>1u\x1b[>1u\x1b[>1u\x1b[>1u\x1b[<u\x1b[<9u\x1b[?u\x1b[=1u",
	"\x1b[>4;2m\x1b[>4m\x1b[>4;1m\x1b[2 q\x1b[!p",
	"\x1b[c\x1b[>0c\x1b[5n\x1b[6n\x1b[>0q\x1b[14t\x1b[16t\x1b[18t\x1b[21t\x1b[22t",
	"\x1b]9;4;1;50\x07\x1b]9;4;x\x07\x1b]10;?\x1b\\\x1b]11;#fff\x07\x1b]12;?\u009c",
	"\x1b]7501;?\x07\x1b]7501;state=blocked;title=Approve?;msg=allow;id=a/b;app=claude\x07",
	"\x1b]7501;app=c;progress=100;kind=auth;id=x;state=idle\x1b\\\x1b]7501;state=idle;state=done\x07",
	"\x1b]0;title\x07\x1b]8;;https://example.com\x07x\x1b]8;;\x07\x1b]133;A\x07\x1b];x\x07",
	"\x1bP$qm\x1b\\\x1b^pm\x1b\\\x1bX" + "sos\x1b\\\u0090x\u009c",
	"clipboard:\x1b]52;c;eA==\x07:end\n",
	"iTerm2 file:\x1b]1337;File=name=eA==;size=1:eA==\x07:end\n",
	"notify:\x1b]9;wb approve: allow evil.example? press y\x07:end\n",
	"ghostty notify:\x1b]777;notify;wb;approve evil.example\x07:end\n",
	"file link:\x1b]8;;file:///System/Applications/Calculator.app\x07calc\x1b]8;;\x07:end\n",
	"kitty graphics:\x1b_Ga=T,t=f;L2V0Yy9ob3N0cw==\x1b\\:end\n",
	"tmux:\x1bP" + "tmux;\x1b\x1b]52;c;eA==\x07\x1b\\:end\n",
	"title report:\x1b[21t:end\nwindow move:\x1b[3;0;0t:end\n",
	"\x1b[1\x18\x1b[1\x1a\x1b[1\x1b[m\x1b]52;c;eA==\x18\x1b]0;a\x1b[1m",
}

// FuzzFilter checks the properties of C10-terminal, "Fuzzing", other
// than the link list (#300) and the exit reset (#301), with the BEL
// limit off. filterChunks checks that no count goes down between
// writes.
func FuzzFilter(f *testing.F) {
	for i, s := range fuzzSeeds {
		f.Add([]byte(s), uint16(i*7))
	}
	f.Fuzz(func(t *testing.T, in []byte, split uint16) {
		out, counts := filterChunks(t, in)
		if err := checkOutput(out); err != nil {
			t.Fatal(err)
		}
		checkIdempotent(t, out)
		checkSplits(t, in, split, out, counts)
	})
}

// checkIdempotent checks that filtering the output again gives the same
// bytes and counts nothing.
func checkIdempotent(t *testing.T, out []byte) {
	t.Helper()
	again, counts := filterChunks(t, out)
	if !bytes.Equal(again, out) {
		t.Fatalf("filtering twice differs:\n once %q\ntwice %q", out, again)
	}
	for _, r := range rules {
		if counts[r] != 0 {
			t.Fatalf("filtering the output again counts %s", r)
		}
	}
}

// checkSplits checks that splitting the input into reads, once at a
// point and once into chunks of 1 to 7 bytes, gives the same output and
// counts.
func checkSplits(t *testing.T, in []byte, split uint16, out []byte, counts map[Rule]uint64) {
	t.Helper()
	i := int(split) % (len(in) + 1)
	splits := [][][]byte{
		{in[:i], in[i:]},
		chunksOf(in, int(split%7)+1),
	}
	for _, chunks := range splits {
		got, gotCounts := filterChunks(t, chunks...)
		if !bytes.Equal(got, out) {
			t.Fatalf("split into %d reads changes the output:\n whole %q\n split %q", len(chunks), out, got)
		}
		if !maps.Equal(gotCounts, counts) {
			t.Fatalf("split into %d reads changes the counts: %v, want %v", len(chunks), gotCounts, counts)
		}
	}
}

// TestCheckerRefuses feeds the property checker outputs that break a
// property, and sees it refuse each. A checker that passes everything
// would make FuzzFilter pass too.
func TestCheckerRefuses(t *testing.T) {
	bad := []string{
		"\xff",
		"a\u009bb",
		"\x05",
		"\x1bc",
		"\x1b[3J",
		"\x1b[3K",
		"\x1b[1;1;1;1;24T",
		"\x1b[1;2S",
		"\x1b]00;x\x07",
		"\x1b]7501;state=idle;id=" + strings.Repeat(strings.Repeat("a", 32)+"/", 4) + "a\x1b\\",
		"\x1b[123456C",
		"\x1b[" + strings.Repeat("1;", 16) + "1m",
		"\x1b[" + strings.Repeat("1", 63) + "m",
		"\x1b[?47h",
		"\x1b[?47$p",
		"\x1b[=1u",
		"\x1b[21t",
		"\x1b[22t",
		strings.Repeat("\x1b[>1u", 5),
		"\x1b[<u",
		"\x1b[>1u\x1b[?1049h\x1b[<u",
		"\x1b]0;x\x07",
		"\x1b]2;x\x1b\\",
		"\x1b]8;;https://example.com\x07",
		"\x1b]52;c;?\x07",
		"\x1b]1337;File=x\x07",
		"\x1b]9;hi\x07",
		"\x1b]9;4;x\x07",
		"\x1b]12;?\u009c",
		"\x1b]7501;state=idle;title=x\x1b\\",
		"\x1b]7501;state=idle\x07",
		"\x1b]7501;id=a;state=idle\x1b\\",
		"\x1bP$qm\x1b\\",
		"\x1b_Ga=q\x1b\\",
		"\x1b^x\x1b\\",
		"\x1bXx\x1b\\",
		"\x1b[",
		"\x1b]10;?",
	}
	for _, b := range bad {
		if checkOutput([]byte(b)) == nil {
			t.Errorf("checkOutput(%q) = nil, want an error", b)
		}
	}
	good := "a\a\x1b7\x1b[2J\x1b[?2026h\x1b[>1u\x1b[<u\x1b]9;4;0;\x07\x1b]7501;state=idle;id=a\x1b\\\x1b[16t"
	if err := checkOutput([]byte(good)); err != nil {
		t.Errorf("checkOutput(%q) = %v, want nil", good, err)
	}
}
