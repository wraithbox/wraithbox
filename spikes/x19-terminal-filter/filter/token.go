// Package filter is the throwaway X19-terminal-filter allowlist filter.
//
// A streaming tokenizer splits terminal output into text, C0 controls,
// escape sequences and strings (OSC, DCS, SOS, PM, APC). The filter
// re-emits only tokens on the allowlist and drops the rest. Because a
// dropped sequence is dropped whole, including its introducer, the host
// terminal's parser is back in its ground state after every token the
// filter emits.
package filter

// Kind is the class of a token.
type Kind int

const (
	Text    Kind = iota // printable UTF-8, no controls
	Control             // a single C0 control, DEL, or a C1 code point
	Invalid             // bytes that are not valid UTF-8
	Esc                 // ESC [intermediates] final
	CSI                 // ESC [ params intermediates final
	OSC                 // ESC ] ... (BEL | ESC \)
	DCS                 // ESC P ... ESC \
	SOS                 // ESC X ... ESC \
	PM                  // ESC ^ ... ESC \
	APC                 // ESC _ ... ESC \
	Aborted             // a sequence cut off by CAN, SUB, ESC, a C0 control, or the length cap
)

func (k Kind) String() string {
	return [...]string{"text", "control", "invalid", "esc", "csi", "osc", "dcs", "sos", "pm", "apc", "aborted"}[k]
}

// Token is one unit of terminal output. Raw holds the bytes as received.
// For Aborted tokens Raw holds the prefix (capped) and Overflow is set
// when the cap was hit.
type Token struct {
	Kind     Kind
	Raw      []byte
	Overflow bool
	// Term is the string terminator for OSC/DCS/SOS/PM/APC: "\a" or "\x1b\\".
	Term string
}

const (
	maxCSI = 64        // bytes from ESC to the final byte
	maxStr = 16 * 1024 // bytes of an OSC or other string
	maxEsc = 8
)

type state int

const (
	sGround state = iota
	sUTF8
	sEsc
	sCSI
	sStr    // inside OSC/DCS/SOS/PM/APC
	sStrEsc // saw ESC inside a string
	sIgnore // over the cap, swallowing until a terminator
	sIgnoreEsc
)

// Tokenizer splits a byte stream into tokens. It keeps state across
// Feed calls, so a sequence split over two reads is one token.
type Tokenizer struct {
	st       state
	buf      []byte
	strKind  Kind
	need     int // remaining UTF-8 continuation bytes
	ignoreIn Kind
	ignoreCS bool // ignoring a CSI (ends at final byte) rather than a string
}

// Feed tokenizes p and calls emit for each complete token. A token's Raw
// may point into the tokenizer's buffer: it is valid only during emit. Text runs are
// emitted at the end of p so that output is not delayed.
func (t *Tokenizer) Feed(p []byte, emit func(Token)) {
	for i := 0; i < len(p); i++ {
		b := p[i]
		switch t.st {
		case sGround:
			switch {
			case b == 0x1b:
				t.flushText(emit)
				t.buf = append(t.buf, b)
				t.st = sEsc
			case b < 0x20 || b == 0x7f:
				t.flushText(emit)
				emit(Token{Kind: Control, Raw: bytesOf[b : int(b)+1]})
			case b < 0x80:
				j := i + 1
				for j < len(p) && p[j] >= 0x20 && p[j] < 0x7f {
					j++
				}
				t.buf = append(t.buf, p[i:j]...)
				i = j - 1
			default:
				t.startUTF8(b, emit)
			}
		case sUTF8:
			if b&0xc0 != 0x80 {
				// truncated sequence: report it and reprocess b
				t.flushInvalidTail(emit)
				t.st = sGround
				i--
				continue
			}
			t.buf = append(t.buf, b)
			t.need--
			if t.need == 0 {
				t.st = sGround
				t.checkLastRune(emit)
			}
		case sEsc:
			t.escByte(b, emit, &i)
		case sCSI:
			t.buf = append(t.buf, b)
			switch {
			case b >= 0x40 && b <= 0x7e:
				t.emitSeq(CSI, emit)
			case b >= 0x20 && b <= 0x3f:
				if len(t.buf) > maxCSI {
					t.abort(emit, true)
					t.st, t.ignoreCS = sIgnore, true
				}
			default:
				// C0, ESC, DEL or 8-bit inside CSI: abort, reprocess b
				t.buf = t.buf[:len(t.buf)-1]
				t.abort(emit, false)
				i--
			}
		case sStr:
			switch {
			case b == 0x07 && t.strKind == OSC:
				t.buf = append(t.buf, b)
				t.emitStr("\a", emit)
			case b == 0x1b:
				t.st = sStrEsc
			case b == 0x18 || b == 0x1a:
				t.abort(emit, false)
			case b < 0x20 && t.strKind == OSC:
				// a C0 control inside OSC: drop the OSC, reprocess b
				t.abort(emit, false)
				i--
			default:
				t.buf = append(t.buf, b)
				if len(t.buf) > maxStr {
					t.ignoreIn = t.strKind
					t.abort(emit, true)
					t.st, t.ignoreCS = sIgnore, false
				}
			}
		case sStrEsc:
			if b == '\\' {
				t.buf = append(t.buf, 0x1b, b)
				t.emitStr("\x1b\\", emit)
				continue
			}
			// ESC inside a string ends it (xterm). Drop the string and
			// start a new escape with b.
			t.abort(emit, false)
			t.buf = append(t.buf, 0x1b)
			t.st = sEsc
			i--
		case sIgnore:
			switch {
			case t.ignoreCS && b >= 0x40 && b <= 0x7e:
				t.st = sGround
			case t.ignoreCS && (b < 0x20 || b > 0x7e):
				t.st = sGround
				i--
			case !t.ignoreCS && b == 0x07:
				t.st = sGround
			case !t.ignoreCS && (b == 0x18 || b == 0x1a):
				t.st = sGround
			case !t.ignoreCS && b == 0x1b:
				t.st = sIgnoreEsc
			}
		case sIgnoreEsc:
			t.st = sGround
			if b != '\\' {
				t.buf = append(t.buf[:0], 0x1b)
				t.st = sEsc
				i--
			}
		}
	}
	switch t.st {
	case sGround:
		t.flushText(emit)
	case sUTF8:
		// emit the text before an incomplete UTF-8 sequence; hold the rest
		start := len(t.buf) - 1
		for start > 0 && t.buf[start]&0xc0 == 0x80 {
			start--
		}
		if start > 0 {
			emit(Token{Kind: Text, Raw: append([]byte(nil), t.buf[:start]...)})
			t.buf = append(t.buf[:0], t.buf[start:]...)
		}
	}
}

func (t *Tokenizer) escByte(b byte, emit func(Token), i *int) {
	t.buf = append(t.buf, b)
	switch {
	case len(t.buf) == 2 && b == '[':
		t.st = sCSI
	case len(t.buf) == 2 && (b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_'):
		t.strKind = map[byte]Kind{']': OSC, 'P': DCS, 'X': SOS, '^': PM, '_': APC}[b]
		t.st = sStr
	case b >= 0x20 && b <= 0x2f:
		if len(t.buf) > maxEsc {
			t.abort(emit, true)
		}
	case b >= 0x30 && b <= 0x7e:
		t.emitSeq(Esc, emit)
	default:
		// C0, ESC, DEL or 8-bit: the escape is cut off; reprocess b
		t.buf = t.buf[:len(t.buf)-1]
		t.abort(emit, false)
		*i--
	}
}

func (t *Tokenizer) startUTF8(b byte, emit func(Token)) {
	switch {
	case b&0xe0 == 0xc0 && b >= 0xc2:
		t.need = 1
	case b&0xf0 == 0xe0:
		t.need = 2
	case b&0xf8 == 0xf0 && b <= 0xf4:
		t.need = 3
	default:
		t.flushText(emit)
		emit(Token{Kind: Invalid, Raw: bytesOf[b : int(b)+1]})
		return
	}
	t.buf = append(t.buf, b)
	t.st = sUTF8
}

// checkLastRune validates the rune just completed at the end of buf:
// overlong forms, surrogates, > U+10FFFF and C1 controls are split out.
func (t *Tokenizer) checkLastRune(emit func(Token)) {
	n := len(t.buf)
	var start int
	for start = n - 1; start > 0 && t.buf[start]&0xc0 == 0x80; start-- {
	}
	r := decode(t.buf[start:])
	switch {
	case r < 0:
		tail := append([]byte(nil), t.buf[start:]...)
		t.buf = t.buf[:start]
		t.flushText(emit)
		emit(Token{Kind: Invalid, Raw: tail})
	case r >= 0x80 && r <= 0x9f:
		tail := append([]byte(nil), t.buf[start:]...)
		t.buf = t.buf[:start]
		t.flushText(emit)
		emit(Token{Kind: Control, Raw: tail})
	}
}

func decode(p []byte) rune {
	var r rune
	switch len(p) {
	case 2:
		r = rune(p[0]&0x1f)<<6 | rune(p[1]&0x3f)
		if r < 0x80 {
			return -1
		}
	case 3:
		r = rune(p[0]&0x0f)<<12 | rune(p[1]&0x3f)<<6 | rune(p[2]&0x3f)
		if r < 0x800 || (r >= 0xd800 && r <= 0xdfff) {
			return -1
		}
	case 4:
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3f)<<12 | rune(p[2]&0x3f)<<6 | rune(p[3]&0x3f)
		if r < 0x10000 || r > 0x10ffff {
			return -1
		}
	default:
		return -1
	}
	return r
}

func (t *Tokenizer) flushInvalidTail(emit func(Token)) {
	var start int
	for start = len(t.buf) - 1; start > 0 && t.buf[start]&0xc0 == 0x80; start-- {
	}
	tail := append([]byte(nil), t.buf[start:]...)
	t.buf = t.buf[:start]
	t.flushText(emit)
	emit(Token{Kind: Invalid, Raw: tail})
}

func (t *Tokenizer) flushText(emit func(Token)) {
	if len(t.buf) == 0 {
		return
	}
	emit(Token{Kind: Text, Raw: t.buf})
	t.buf = t.buf[:0]
}

func (t *Tokenizer) emitSeq(k Kind, emit func(Token)) {
	emit(Token{Kind: k, Raw: t.buf})
	t.buf = t.buf[:0]
	t.st = sGround
}

func (t *Tokenizer) emitStr(term string, emit func(Token)) {
	emit(Token{Kind: t.strKind, Raw: t.buf, Term: term})
	t.buf = t.buf[:0]
	t.st = sGround
}

func (t *Tokenizer) abort(emit func(Token), overflow bool) {
	emit(Token{Kind: Aborted, Raw: t.buf, Overflow: overflow})
	t.buf = t.buf[:0]
	t.st = sGround
}

// Pending reports bytes held back waiting for the rest of a sequence.
func (t *Tokenizer) Pending() int { return len(t.buf) }

// bytesOf holds every byte value, so a one-byte token needs no allocation.
var bytesOf = func() (a [256]byte) {
	for i := range a {
		a[i] = byte(i)
	}
	return a
}()
