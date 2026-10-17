package termfilter

import (
	"slices"
	"strings"
)

// Caps for OSC values that S16-01-filter leaves to the code.
const (
	// maxOSCNumberDigits bounds the significant digits of an OSC number
	// read for counting. Leading zeros don't count, so `OSC 052` counts
	// as OSC 52 (S16-02-drops-and-links, "Drop rules").
	maxOSCNumberDigits = 9
	// maxProgressValues and maxProgressDigits bound the parameters of
	// an OSC 9 ; 4 progress report after its 4 (S16-01-filter,
	// "Allowlist", row "OSC"). X19-terminal-filter recorded two, the
	// longest of one digit.
	maxProgressValues = 3
	maxProgressDigits = 3
	// maxStatusBytes bounds an OSC 7501 report, from its ESC to the end
	// of its terminator (S16-01-filter, "Program status").
	maxStatusBytes = 4096
)

// stTerminator is ST written as `ESC \`.
var stTerminator = []byte{esc, '\\'}

// oscNumber is the number before the first `;` of an OSC.
type oscNumber struct {
	value int
	// known is set when the number has only digits, and at most
	// maxOSCNumberDigits of them after its leading zeros.
	known bool
	// plain is set when the number is known and written without a
	// leading zero. Only a plain number passes.
	plain bool
}

// readOSC splits the content of an OSC, after its `ESC ]`, into its
// number and the payload after the first `;`.
func readOSC(content string) (oscNumber, string) {
	num, payload, _ := strings.Cut(content, ";")
	sig := strings.TrimLeft(num, "0")
	if sig == "" && num != "" {
		sig = "0"
	}
	if len(sig) > maxOSCNumberDigits {
		return oscNumber{}, payload
	}
	v, ok := number(sig)
	if !ok || !isDigits(num) {
		return oscNumber{}, payload
	}
	return oscNumber{value: v, known: true, plain: sig == num}, payload
}

// isDigits reports whether s is one digit or more, and nothing else.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// oscDropRule is the rule a dropped OSC counts under, by its number
// (S16-02-drops-and-links). An OSC without a number counts as RuleOSC.
func oscDropRule(n oscNumber) Rule {
	if !n.known {
		return RuleOSC
	}
	switch n.value {
	case 0, 1, 2:
		// Titles reach desktop notifications through the bell
		// (SEC14-no-fake-approvals).
		return RuleOSCTitle
	case 8:
		return RuleOSCLink
	case 52:
		// Clipboard writes, and `?` reads it back into the terminal's
		// input (SEC03-no-host-exec).
		return RuleOSCClipboard
	case 7, 1337, 5113:
		// The current directory, iTerm2 files and kitty file transfer
		// (SEC03-no-host-exec).
		return RuleOSCFile
	}
	return RuleOSC
}

// osc decides on an OSC. Only OSC 9 ; 4, OSC 10 to 12 with `?`, and OSC
// 7501 pass, and OSC 9, 99 and 777 become a BEL (S16-01-filter,
// "Allowlist"). Every other OSC is dropped.
func (f *Filter) osc(t token) {
	n, payload := readOSC(string(t.raw[2:]))
	if n.known && n.value == 8 {
		// Every OSC 8 is dropped, whatever its URL and its mark, and its
		// text stays as plain text. The filter keeps the link for the
		// record (SEC03-no-host-exec, S16-02-drops-and-links "Links").
		f.count(RuleOSCLink)
		f.links.osc8(payload, t.cut)
		return
	}
	if t.cut || !n.plain {
		f.count(oscDropRule(n))
		return
	}
	switch n.value {
	case 9:
		f.osc9(t, payload)
	case 99, 777:
		f.notify()
	case 10, 11, 12:
		f.passIf(t, payload == "?", RuleOSC)
	case 7501:
		f.status(t, payload)
	default:
		f.count(oscDropRule(n))
	}
}

// osc9 passes a progress report, `OSC 9 ; 4` with numeric parameters,
// and turns any other OSC 9 into a BEL. A progress report with other
// parameters is malformed, not a notification, and is dropped
// (S16-02-drops-and-links, "Which rule a drop counts under").
func (f *Filter) osc9(t token, payload string) {
	if payload != "4" && !strings.HasPrefix(payload, "4;") {
		f.notify()
		return
	}
	f.passIf(t, progressParams(payload[1:]), RuleOSC)
}

// progressParams reports whether rest, the part after `4`, is at most
// maxProgressValues values of at most maxProgressDigits digits, each
// after a `;`. A value may be empty.
func progressParams(rest string) bool {
	if rest == "" {
		return true
	}
	values := strings.Split(rest[1:], ";")
	if len(values) > maxProgressValues {
		return false
	}
	for _, v := range values {
		if len(v) > maxProgressDigits || (v != "" && !isDigits(v)) {
			return false
		}
	}
	return true
}

// notify turns a notification into one BEL, which goes through the BEL
// limit. Guest text never reaches a desktop notification, where it
// could pass for an approval (SEC14-no-fake-approvals).
func (f *Filter) notify() {
	f.count(RuleOSCNotify)
	f.bel()
}

// passIf passes the OSC as it came when ok, and otherwise counts it
// under rule.
func (f *Filter) passIf(t token, ok bool, rule Rule) {
	if !ok {
		f.count(rule)
		return
	}
	f.passString(t)
}

// passString writes a string sequence with its terminator. One that
// ended with U+009C is written with `ESC \` instead, because a
// terminal that doesn't read U+009C as ST would take the next bytes as
// its payload (S16-01-filter, "Ends of a sequence").
func (f *Filter) passString(t token) {
	f.pass(t.raw)
	switch t.term {
	case termBEL:
		f.out = append(f.out, bel)
	case termST, termC1ST, termNone:
		f.pass(stTerminator)
	}
}

// termLen is the length of a terminator as it came.
func termLen(tm term) int {
	switch tm {
	case termBEL:
		return 1
	case termST, termC1ST:
		return 2
	case termNone:
	}
	return 0
}

// statusKeys are the keys an OSC 7501 report keeps, in the order the
// rebuilt report lists them, with the form of each value
// (S16-01-filter, "Program status").
var statusKeys = []struct {
	key   string
	valid func(string) bool
}{
	{"state", oneOfValues("idle", "working", "done", "blocked", "error", "clear")},
	{"id", statusID},
	{"kind", oneOfValues("permission", "question", "auth")},
	{"progress", statusProgress},
	{"app", statusSegment},
}

// status passes the query `OSC 7501 ; ?` as it came, and rebuilds a
// report from the keys it keeps. `title`, `msg` and every other key are
// removed: they are guest text that a terminal may show in a desktop
// notification (SEC14-no-fake-approvals). A report outside its form is
// dropped.
func (f *Filter) status(t token, payload string) {
	if len(t.raw)+termLen(t.term) > maxStatusBytes {
		f.count(RuleOSC)
		return
	}
	if payload == "?" {
		f.passString(t)
		return
	}
	pairs, removed, ok := readStatus(payload)
	if !ok {
		f.count(RuleOSC)
		return
	}
	if removed {
		f.count(RuleOSCStatusText)
	}
	f.out = append(f.out, esc, ']')
	f.out = append(f.out, "7501"...)
	for _, k := range statusKeys {
		if v, kept := pairs[k.key]; kept {
			f.out = append(f.out, ';')
			f.out = append(f.out, k.key...)
			f.out = append(f.out, '=')
			f.out = append(f.out, v...)
		}
	}
	f.pass(stTerminator)
}

// readStatus reads the `key=value` pairs of a report, separated by
// `;`. It returns the kept pairs, and whether it removed one. It fails
// on a pair without `=`, an empty key or value, a value with `=`, a
// repeated key, a kept value outside its form, or a missing `state`.
func readStatus(payload string) (map[string]string, bool, bool) {
	seen := map[string]bool{}
	kept := map[string]string{}
	removed := false
	for pair := range strings.SplitSeq(payload, ";") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" || value == "" || strings.Contains(value, "=") || seen[key] {
			return nil, false, false
		}
		seen[key] = true
		valid, keep := statusForm(key)
		if !keep {
			removed = true
			continue
		}
		if !valid(value) {
			return nil, false, false
		}
		kept[key] = value
	}
	_, hasState := kept["state"]
	return kept, removed, hasState
}

func statusForm(key string) (func(string) bool, bool) {
	for _, k := range statusKeys {
		if k.key == key {
			return k.valid, true
		}
	}
	return nil, false
}

func oneOfValues(allowed ...string) func(string) bool {
	return func(v string) bool {
		return slices.Contains(allowed, v)
	}
}

// Forms of OSC 7501 values (S16-01-filter, "Program status").
const (
	maxStatusIDBytes    = 128
	maxStatusIDSegments = 8
	maxStatusSegment    = 32
)

// statusID passes 1 to 8 segments joined by `/`, at most 128 bytes.
func statusID(v string) bool {
	if len(v) > maxStatusIDBytes {
		return false
	}
	segments := strings.Split(v, "/")
	if len(segments) > maxStatusIDSegments {
		return false
	}
	for _, s := range segments {
		if !statusSegment(s) {
			return false
		}
	}
	return true
}

// statusSegment passes 1 to 32 characters from `A-Z`, `a-z`, `0-9` and
// `_ . + -`.
func statusSegment(s string) bool {
	if s == "" || len(s) > maxStatusSegment {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isDigit(c) && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && !strings.ContainsRune("_.+-", rune(c)) {
			return false
		}
	}
	return true
}

// statusProgress passes an integer from 0 to 100, in its plain form.
func statusProgress(v string) bool {
	n, ok := number(v)
	return ok && n <= 100 && (v == "0" || v[0] != '0')
}
