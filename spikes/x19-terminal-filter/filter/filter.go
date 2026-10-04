package filter

import (
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Decision is the filter's verdict on one token, with the rule that made it.
type Decision struct {
	Pass    bool
	Rule    string
	Rewrite []byte // when set, emit this instead of the token
}

func pass(rule string) Decision { return Decision{Pass: true, Rule: rule} }
func drop(rule string) Decision { return Decision{Rule: rule} }

var replacement = []byte("�")

// Decide applies the allowlist to one token.
func Decide(t Token) Decision {
	switch t.Kind {
	case Text:
		return pass("text")
	case Control:
		if len(t.Raw) == 1 {
			switch t.Raw[0] {
			case 0x07, 0x08, 0x09, 0x0a, 0x0d, 0x0f:
				// BEL BS HT LF CR, and SI (back to the G0 character set)
				return pass("c0-allowed")
			}
			return drop("c0-not-allowed")
		}
		return drop("c1-control")
	case Invalid:
		return Decision{Pass: true, Rule: "invalid-utf8-replaced", Rewrite: replacement}
	case Esc:
		return decideEsc(t)
	case CSI:
		return decideCSI(t)
	case OSC:
		return decideOSC(t)
	case DCS:
		return drop("dcs")
	case SOS, PM:
		return drop("sos-pm")
	case APC:
		return drop("apc")
	case Aborted:
		if t.Overflow {
			return drop("over-length")
		}
		return drop("malformed")
	}
	return drop("unknown")
}

var escAllowed = map[string]string{
	"7": "decsc", "8": "decrc", "M": "ri", "D": "ind", "E": "nel",
	"=": "deckpam", ">": "deckpnm", "(B": "charset", "(0": "charset", ")0": "charset", ")B": "charset",
}

func decideEsc(t Token) Decision {
	if r, ok := escAllowed[string(t.Raw[1:])]; ok {
		return pass("esc-" + r)
	}
	return drop("esc-not-allowed")
}

// Modes a guest may set or reset (DECSET/DECRST) or query (DECRQM).
var privateModes = map[string]bool{
	"1": true, "7": true, "12": true, "25": true, "47": true, "1047": true, "1048": true, "1049": true,
	"1000": true, "1002": true, "1003": true, "1006": true, "1004": true, "2004": true,
	"1016": true, "2026": true, "2027": true, "2031": true,
}

var digits = regexp.MustCompile(`^[0-9;:]*$`)

func decideCSI(t Token) Decision {
	if isPlainSGR(t.Raw) {
		return pass("csi-sgr") // fast path: most of Claude Code's output
	}
	prefix, params, inter, final := CSIParts(t.Raw)
	if !digits.MatchString(params) {
		return drop("csi-bad-params")
	}
	key := prefix + inter + string(final)
	switch key {
	case "m":
		return pass("csi-sgr")
	case "A", "B", "C", "D", "E", "F", "G", "H", "f", "J", "K", "L", "M", "P", "@", "X", "S", "T",
		"d", "e", "a", "`", "b", "r", "g", "I", "Z":
		return pass("csi-cursor-edit")
	case "s", "u":
		if params == "" {
			return pass("csi-save-restore")
		}
		return drop("csi-not-allowed")
	case "c", ">c":
		if params == "" || params == "0" {
			return pass("csi-device-attributes")
		}
	case "n":
		if params == "5" || params == "6" {
			return pass("csi-status-report")
		}
	case "?$p":
		// DECRQM only reads a mode; the reply goes to the guest
		return pass("csi-mode-query")
	case "?h", "?l":
		for _, m := range strings.Split(params, ";") {
			if !privateModes[m] {
				return drop("csi-mode-not-allowed")
			}
		}
		return pass("csi-private-mode")
	case "?u", ">u", "<u", "=u":
		return pass("csi-kitty-keyboard")
	case ">q":
		return pass("csi-xtversion")
	case ">m":
		return pass("csi-modify-other-keys")
	case " q":
		return pass("csi-cursor-style")
	case "!p":
		return pass("csi-soft-reset")
	case "t":
		// title stack only; window moves, resizes and reports stay out
		f := strings.Split(params, ";")
		if f[0] == "22" || f[0] == "23" {
			return pass("csi-title-stack")
		}
		if params == "14" || params == "16" || params == "18" {
			// size reports in pixels or cells; the reply goes to the guest
			return pass("csi-size-report")
		}
		return drop("csi-window-ops")
	}
	return drop("csi-not-allowed")
}

func decideOSC(t Token) Decision {
	num, payload := OSCParts(t)
	switch num {
	case "0", "1", "2":
		if !cleanText(payload, 512) {
			return drop("osc-title-unclean")
		}
		return pass("osc-title")
	case "8":
		params, uri, ok := strings.Cut(payload, ";")
		if !ok || len(params) > 256 || !cleanASCII(params) {
			return drop("osc8-malformed")
		}
		if uri == "" {
			return pass("osc8-close")
		}
		if len(uri) > 2048 || !cleanASCII(uri) || strings.ContainsAny(uri, " ") {
			return drop("osc8-malformed")
		}
		l := strings.ToLower(uri)
		if strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") {
			return pass("osc8-http")
		}
		// file://, x-man-page://, custom app schemes: the host would open
		// a guest-chosen local resource (SEC03-no-host-exec).
		return drop("osc8-scheme")
	case "9":
		if strings.HasPrefix(payload, "4;") && digits.MatchString(payload[2:]) {
			return pass("osc9-progress")
		}
		// iTerm2 desktop notification: guest text in a host notification
		// could pass for an approval (SEC14-no-fake-approvals). The bell
		// keeps the attention signal without the text.
		return Decision{Pass: true, Rule: "osc-notification-to-bel", Rewrite: []byte{0x07}}
	case "99", "777":
		return Decision{Pass: true, Rule: "osc-notification-to-bel", Rewrite: []byte{0x07}}
	case "10", "11", "12":
		if payload == "?" {
			return pass("osc-color-query")
		}
		return drop("osc-color-set")
	case "52":
		return drop("osc52-clipboard") // FR12-clipboard
	case "1337":
		return drop("osc1337-iterm2")
	case "7":
		return drop("osc7-cwd")
	}
	return drop("osc-not-allowed")
}

func cleanASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 && s[i] != ' ' || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func cleanText(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return false
		}
	}
	return true
}

// Stats counts dropped tokens by rule.
type Stats struct {
	In, Out int64
	Dropped map[string]int
	Changed map[string]int // dropped or rewritten
}

// Filter is an io.Writer that writes only allowed tokens to W.
type Filter struct {
	W      io.Writer
	OnDrop func(Token, Decision) // optional log hook
	tok    Tokenizer
	out    []byte
	Stats  Stats
}

func New(w io.Writer) *Filter {
	return &Filter{W: w, Stats: Stats{Dropped: map[string]int{}, Changed: map[string]int{}}}
}

func (f *Filter) Write(p []byte) (int, error) {
	f.Stats.In += int64(len(p))
	f.out = f.out[:0]
	f.tok.Feed(p, f.handle)
	f.Stats.Out += int64(len(f.out))
	if len(f.out) > 0 {
		if _, err := f.W.Write(f.out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (f *Filter) handle(t Token) {
	d := Decide(t)
	switch {
	case d.Rewrite != nil:
		f.out = append(f.out, d.Rewrite...)
	case d.Pass:
		f.out = append(f.out, t.Raw...)
	default:
		f.Stats.Dropped[d.Rule]++
	}
	if !d.Pass || d.Rewrite != nil {
		f.Stats.Changed[d.Rule]++
		if f.OnDrop != nil {
			f.OnDrop(t, d)
		}
	}
}

// Pending reports bytes held back for an incomplete sequence.
func (f *Filter) Pending() int { return f.tok.Pending() }

// Apply filters a whole buffer, for tests and replays.
func Apply(p []byte) []byte {
	var b strings.Builder
	f := New(&b)
	f.Write(p)
	return []byte(b.String())
}

func isPlainSGR(raw []byte) bool {
	if raw[len(raw)-1] != 'm' {
		return false
	}
	for _, c := range raw[2 : len(raw)-1] {
		if (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}
