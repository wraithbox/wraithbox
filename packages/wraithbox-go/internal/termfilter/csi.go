package termfilter

import (
	"slices"
	"strconv"
	"strings"
)

// CSI parameter caps of S16-01-filter, "Allowlist", row "CSI
// parameters". Both `;` and `:` separate values, so a `:` sub-parameter
// counts as a value. The terminal emulator's parser is one more thing
// these protect.
const (
	maxCSIValues      = 16
	maxCSIValueDigits = 5
)

// kittyDepth is how deep the guest may push the kitty keyboard stack
// on one screen (S16-01-filter, "Allowlist", row "CSI keyboard").
const kittyDepth = 4

// modes are the private modes the guest may set, reset and query
// (S16-01-filter, "Allowlist", row "CSI modes"), in their plain form.
var modes = map[string]bool{
	"25": true, "1000": true, "1002": true, "1003": true, "1004": true, "1006": true,
	"1016": true, "1049": true, "2004": true, "2026": true, "2031": true,
}

// altScreen is the only screen switch on the list.
const altScreen = "1049"

// csiForm is a CSI split into its parts: an optional private prefix
// (`<`, `=`, `>` or `?`), the parameters, the intermediates and the
// final byte.
type csiForm struct {
	prefix byte
	params string
	inter  string
	final  byte
}

// csiKey selects the rule for a CSI.
type csiKey struct {
	prefix byte
	inter  string
	final  byte
}

// csiCheck decides on the parameters of a CSI whose key matched. A
// check that passes may change the filter's state.
type csiCheck func(f *Filter, params string) bool

// csiRules is the allowlist of S16-01-filter for CSI, by key. A key
// that isn't here is dropped.
var csiRules = func() map[csiKey]csiCheck {
	m := map[csiKey]csiCheck{
		// Rows "CSI screen".
		{final: 'J'}: eraseParam,
		{final: 'K'}: eraseParam,
		{final: 's'}: noParams,
		{final: 'u'}: noParams,
		// Row "CSI modes".
		{prefix: '?', final: 'h'}:             setModes(true),
		{prefix: '?', final: 'l'}:             setModes(false),
		{prefix: '?', inter: "$", final: 'p'}: queryMode,
		// Row "CSI keyboard".
		{prefix: '>', final: 'u'}: kittyPush,
		{prefix: '<', final: 'u'}: kittyPop,
		{prefix: '?', final: 'u'}: noParams,
		{prefix: '>', final: 'm'}: oneOf("4;2", "4"),
		{inter: " ", final: 'q'}:  anyParams,
		{inter: "!", final: 'p'}:  noParams,
		// Row "CSI queries". `CSI 21 t`, the title report, is never on
		// the list (SEC03-no-host-exec, S16-01-filter "Never on the
		// list"), and neither is any other t.
		{final: 'c'}:              oneOf("", "0"),
		{prefix: '>', final: 'c'}: oneOf("", "0"),
		{final: 'n'}:              oneOf("5", "6"),
		{prefix: '>', final: 'q'}: oneOf("", "0"),
		{final: 't'}:              oneOf("14", "16", "18"),
	}
	for _, b := range []byte("ABCDEFGHf`ade@LMPXbSTrIZgm") {
		m[csiKey{final: b}] = anyParams
	}
	return m
}()

// allowCSI reports whether a complete CSI passes. The parameter caps
// guard the terminal emulator's parser. The keys keep `CSI 3 J`, which
// erases the host's scroll-back, window moves, and the title report,
// which types guest text into the terminal's input, off the host
// (SEC03-no-host-exec).
func (f *Filter) allowCSI(raw []byte) bool {
	c, ok := parseCSI(raw)
	if !ok || !withinCaps(c.params) {
		return false
	}
	check, ok := csiRules[csiKey{prefix: c.prefix, inter: c.inter, final: c.final}]
	return ok && check(f, c.params)
}

// parseCSI splits a CSI into its parts. It fails when a parameter
// holds a byte other than a digit, `;` or `:`, or when the bytes are
// out of order.
func parseCSI(raw []byte) (csiForm, bool) {
	body := string(raw[2 : len(raw)-1])
	c := csiForm{final: raw[len(raw)-1]}
	if body != "" && strings.IndexByte("<=>?", body[0]) >= 0 {
		c.prefix = body[0]
		body = body[1:]
	}
	n := 0
	for n < len(body) && (isDigit(body[n]) || body[n] == ';' || body[n] == ':') {
		n++
	}
	c.params, body = body[:n], body[n:]
	n = 0
	for n < len(body) && body[n] >= 0x20 && body[n] <= 0x2f {
		n++
	}
	c.inter = body[:n]
	return c, n == len(body)
}

// withinCaps reports whether the parameters hold at most maxCSIValues
// values of at most maxCSIValueDigits digits each.
func withinCaps(params string) bool {
	if params == "" {
		return true
	}
	values := 1
	digits := 0
	for i := 0; i < len(params); i++ {
		if params[i] == ';' || params[i] == ':' {
			values++
			digits = 0
			continue
		}
		digits++
		if digits > maxCSIValueDigits {
			return false
		}
	}
	return values <= maxCSIValues
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// number reads s as a decimal number of digits only. It fails on an
// empty string and on anything else.
func number(s string) (int, bool) {
	if !isDigits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func anyParams(*Filter, string) bool { return true }

func noParams(_ *Filter, params string) bool { return params == "" }

func oneOf(allowed ...string) csiCheck {
	return func(_ *Filter, params string) bool {
		return slices.Contains(allowed, params)
	}
}

// eraseParam passes `J` and `K` with no parameter or one from 0 to 2.
// `CSI 3 J` erases the host's scroll-back.
func eraseParam(_ *Filter, params string) bool {
	if params == "" {
		return true
	}
	n, ok := number(params)
	return ok && n <= 2
}

// setModes passes `? h` and `? l` when every mode is on the list. A
// switch to or from the alternate screen moves the kitty keyboard count
// to that screen.
func setModes(set bool) csiCheck {
	return func(f *Filter, params string) bool {
		if params == "" {
			return false
		}
		screen := false
		for m := range strings.SplitSeq(params, ";") {
			if !modes[m] {
				return false
			}
			screen = screen || m == altScreen
		}
		if screen {
			f.kb.alt = set
		}
		return true
	}
}

// queryMode passes the mode query `? … $ p` for one mode on the list.
func queryMode(_ *Filter, params string) bool {
	return modes[params]
}

// kittyPush passes `CSI > flags u` while the guest's pushes on the
// current screen are fewer than kittyDepth.
func kittyPush(f *Filter, params string) bool {
	if params != "" {
		if _, ok := number(params); !ok {
			return false
		}
	}
	return f.kb.push()
}

// kittyPop passes `CSI < n u` when the guest pushed at least n entries
// on the current screen, so a pop never reaches an entry the user's
// shell pushed. No number pops one. The number is in its plain form,
// and not 0.
func kittyPop(f *Filter, params string) bool {
	n := 1
	if params != "" {
		v, ok := number(params)
		if !ok || v == 0 || params[0] == '0' {
			return false
		}
		n = v
	}
	return f.kb.pop(n)
}

// keyboard is the guest's kitty keyboard state (S16-01-filter, "Kitty
// keyboard stacks"): which screen is active, and how many entries the
// guest pushed on each. The exit reset of S16-03-around-the-stream
// needs it to pop the guest's pushes.
type keyboard struct {
	alt    bool
	pushes [2]int // main, alternate
}

func (k *keyboard) screen() int {
	if k.alt {
		return 1
	}
	return 0
}

func (k *keyboard) push() bool {
	s := k.screen()
	if k.pushes[s] >= kittyDepth {
		return false
	}
	k.pushes[s]++
	return true
}

func (k *keyboard) pop(n int) bool {
	s := k.screen()
	if n > k.pushes[s] {
		return false
	}
	k.pushes[s] -= n
	return true
}
