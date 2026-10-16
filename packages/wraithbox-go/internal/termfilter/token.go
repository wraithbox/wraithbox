package termfilter

// Buffer caps of S16-01-filter, "Ends of a sequence". They bound what
// the filter holds of one unfinished sequence (SEC13-bounded-resources).
const (
	// maxCSI is the most bytes of a CSI, from its ESC to its final byte.
	maxCSI = 64
	// maxString is the most bytes of an unfinished OSC, DCS, SOS, PM or
	// APC, from its ESC, that the filter holds.
	maxString = 16 * 1024
	// maxESC is the most bytes of an ESC sequence, from its ESC to its
	// final byte. The longest one the allowlist passes has 3.
	maxESC = 8
)

// Bytes the tokenizer treats apart.
const (
	bel = 0x07
	can = 0x18
	sub = 0x1a
	esc = 0x1b
	del = 0x7f
	// c2 is the first byte of the UTF-8 form of every C1 code point.
	c2 = 0xc2
	// st8 is the second byte of U+009C, the 8-bit string terminator.
	st8 = 0x9c
)

// kind is the class of a token.
type kind uint8

const (
	kText    kind = iota // valid UTF-8 without controls or C1 code points
	kInvalid             // one byte that isn't part of a valid UTF-8 character
	kC1                  // one C1 code point, U+0080 to U+009F, as UTF-8
	kControl             // one C0 control or DEL
	kESC                 // ESC, intermediates, final byte
	kCSI                 // ESC [, parameters, intermediates, final byte
	kOSC                 // ESC ], ended by BEL or ST
	kDCS                 // ESC P, ended by ST
	kSOS                 // ESC X, ended by ST
	kPM                  // ESC ^, ended by ST
	kAPC                 // ESC _, ended by ST
)

// term is how a string sequence ended.
type term uint8

const (
	termNone term = iota // not a string sequence, or cut
	termBEL              // BEL, which ends only an OSC
	termST               // ESC \
	termC1ST             // U+009C
)

// token is one unit of the stream. raw holds the bytes as received, and
// is valid only during the callback that gets the token. For a string
// sequence, raw runs from its ESC to just before its terminator.
type token struct {
	kind kind
	raw  []byte
	term term
	// cut is set on a sequence that ended before its own end: at a
	// buffer cap, at CAN, SUB or ESC, at a byte that doesn't fit, or at
	// the end of the stream. raw then holds its first bytes, at most up
	// to the cap.
	cut bool
}

type state uint8

const (
	stGround    state = iota
	stUTF8            // inside a multi-byte UTF-8 character
	stESC             // after ESC
	stCSI             // after ESC [
	stString          // inside OSC, DCS, SOS, PM or APC
	stStringESC       // after ESC inside a string
	stStringC2        // after 0xC2 inside a string, which may start U+009C
)

// tokenizer splits a byte stream into tokens, keeping state across feed
// calls, so a token split over two reads is one token. The rules for
// where a sequence ends are those of S16-01-filter, "Ends of a sequence".
type tokenizer struct {
	st   state
	text []byte // text not yet emitted
	seq  []byte // the sequence so far
	sk   kind   // the class of seq
	// discard is set once seq passed its cap: the cut token is emitted,
	// and the rest of the sequence is read without being kept.
	discard bool

	u8     [4]byte // an unfinished UTF-8 character
	u8n    int
	u8need int
	u8lo   byte // the range of the next continuation byte
	u8hi   byte
}

// feed splits p into tokens and calls emit for each complete one. At
// the end of p it emits the text it holds, so text in front of an
// unfinished character or sequence is never delayed.
func (t *tokenizer) feed(p []byte, emit func(token)) {
	for i := 0; i < len(p); {
		if t.st == stGround {
			if n := textRun(p[i:]); n > 0 {
				t.text = append(t.text, p[i:i+n]...)
				i += n
				continue
			}
		}
		if t.step(p[i], emit) {
			i++
		}
	}
	t.flushText(emit)
}

// end ends the stream: the bytes of an unfinished UTF-8 character are
// each invalid, and an unfinished sequence is cut.
func (t *tokenizer) end(emit func(token)) {
	switch t.st {
	case stGround:
	case stUTF8:
		t.invalidUTF8(emit)
	case stESC, stCSI, stString, stStringESC, stStringC2:
		t.cut(emit)
	}
	t.flushText(emit)
}

// idle reports whether the tokenizer holds nothing.
func (t *tokenizer) idle() bool {
	return t.st == stGround && len(t.text) == 0
}

// textRun returns how many bytes at the start of p are printable ASCII.
func textRun(p []byte) int {
	for i, b := range p {
		if b < 0x20 || b >= del {
			return i
		}
	}
	return len(p)
}

// step reads one byte, and reports whether it used it. A byte it didn't
// use ended the token before it and is read again.
func (t *tokenizer) step(b byte, emit func(token)) bool {
	switch t.st {
	case stGround:
		t.ground(b, emit)
		return true
	case stUTF8:
		return t.utf8(b, emit)
	case stESC:
		return t.escape(b, emit)
	case stCSI:
		return t.csi(b, emit)
	case stString:
		t.str(b, emit)
		return true
	case stStringESC:
		return t.strESC(b, emit)
	case stStringC2:
		return t.strC2(b, emit)
	}
	return true
}

func (t *tokenizer) ground(b byte, emit func(token)) {
	switch {
	case b == esc:
		t.flushText(emit)
		t.seq = append(t.seq[:0], b)
		t.sk = kESC
		t.st = stESC
	case b < 0x20 || b == del:
		t.flushText(emit)
		emit(token{kind: kControl, raw: byteValues[b : int(b)+1]})
	case b < 0x80:
		t.text = append(t.text, b)
	default:
		t.startUTF8(b, emit)
	}
}

// startUTF8 reads the first byte of a multi-byte character. The ranges
// are those of well-formed UTF-8 (Unicode, table 3-7), so an overlong
// form, a surrogate or a code point past U+10FFFF is invalid at the
// first byte that shows it.
func (t *tokenizer) startUTF8(b byte, emit func(token)) {
	lo, hi := byte(0x80), byte(0xbf)
	switch {
	case b >= 0xc2 && b <= 0xdf:
		t.u8need = 1
	case b == 0xe0:
		t.u8need, lo = 2, 0xa0
	case b == 0xed:
		t.u8need, hi = 2, 0x9f
	case b >= 0xe1 && b <= 0xef:
		t.u8need = 2
	case b == 0xf0:
		t.u8need, lo = 3, 0x90
	case b == 0xf4:
		t.u8need, hi = 3, 0x8f
	case b >= 0xf1 && b <= 0xf3:
		t.u8need = 3
	default:
		t.flushText(emit)
		emit(token{kind: kInvalid, raw: byteValues[b : int(b)+1]})
		return
	}
	t.u8[0], t.u8n = b, 1
	t.u8lo, t.u8hi = lo, hi
	t.st = stUTF8
}

func (t *tokenizer) utf8(b byte, emit func(token)) bool {
	if b < t.u8lo || b > t.u8hi {
		t.invalidUTF8(emit)
		return false
	}
	t.u8[t.u8n] = b
	t.u8n++
	t.u8need--
	t.u8lo, t.u8hi = 0x80, 0xbf
	if t.u8need > 0 {
		return true
	}
	t.st = stGround
	if t.u8n == 2 && t.u8[0] == c2 && t.u8[1] <= 0x9f {
		t.flushText(emit)
		emit(token{kind: kC1, raw: t.u8[:2]})
		return true
	}
	t.text = append(t.text, t.u8[:t.u8n]...)
	return true
}

// invalidUTF8 emits each byte of an unfinished character as invalid.
func (t *tokenizer) invalidUTF8(emit func(token)) {
	t.flushText(emit)
	for _, b := range t.u8[:t.u8n] {
		emit(token{kind: kInvalid, raw: byteValues[b : int(b)+1]})
	}
	t.u8n, t.u8need = 0, 0
	t.st = stGround
}

var stringKinds = map[byte]kind{']': kOSC, 'P': kDCS, 'X': kSOS, '^': kPM, '_': kAPC}

func (t *tokenizer) escape(b byte, emit func(token)) bool {
	first := len(t.seq) == 1 && !t.discard
	sk, isString := stringKinds[b]
	switch {
	case first && b == '[':
		t.seq = append(t.seq, b)
		t.sk, t.st = kCSI, stCSI
	case first && isString:
		t.seq = append(t.seq, b)
		t.sk, t.st = sk, stString
	case b >= 0x20 && b <= 0x2f:
		t.add(b, maxESC, emit)
	case b >= 0x30 && b <= 0x7e:
		t.add(b, maxESC, emit)
		t.finish(termNone, emit)
	case b == can || b == sub:
		t.cut(emit)
	default:
		t.cut(emit)
		return false
	}
	return true
}

// csi reads a byte of a CSI. Any byte that isn't a parameter,
// intermediate or final byte ends it, and the CSI is cut
// (S16-01-filter, "Ends of a sequence"). CAN and SUB are part of the
// cut sequence. Another byte starts the next token.
func (t *tokenizer) csi(b byte, emit func(token)) bool {
	switch {
	case b >= 0x20 && b <= 0x3f:
		t.add(b, maxCSI, emit)
	case b >= 0x40 && b <= 0x7e:
		t.add(b, maxCSI, emit)
		t.finish(termNone, emit)
	case b == can || b == sub:
		t.cut(emit)
	default:
		t.cut(emit)
		return false
	}
	return true
}

func (t *tokenizer) str(b byte, emit func(token)) {
	switch {
	case b == bel && t.sk == kOSC:
		t.finish(termBEL, emit)
	case b == esc:
		t.st = stStringESC
	case b == c2:
		t.st = stStringC2
	case b == can || b == sub:
		t.cut(emit)
	default:
		t.add(b, maxString, emit)
	}
}

// strESC reads the byte after an ESC inside a string. `ESC \` is ST.
// Any other byte means the ESC ended the string and starts the next
// token.
func (t *tokenizer) strESC(b byte, emit func(token)) bool {
	if b == '\\' {
		t.finish(termST, emit)
		return true
	}
	t.cut(emit)
	t.seq = append(t.seq[:0], esc)
	t.sk, t.st = kESC, stESC
	return false
}

// strC2 reads the byte after 0xC2 inside a string. 0xC2 0x9C is U+009C,
// which is ST. Otherwise 0xC2 is part of the string.
func (t *tokenizer) strC2(b byte, emit func(token)) bool {
	if b == st8 {
		t.finish(termC1ST, emit)
		return true
	}
	t.st = stString
	t.add(c2, maxString, emit)
	return false
}

// add appends b to the sequence. Past the cap it emits the sequence cut
// once, and keeps nothing more of it.
func (t *tokenizer) add(b byte, limit int, emit func(token)) {
	if t.discard {
		return
	}
	if len(t.seq) >= limit {
		emit(token{kind: t.sk, raw: t.seq, cut: true})
		t.discard = true
		return
	}
	t.seq = append(t.seq, b)
}

// finish emits the sequence, unless it was cut at its cap, and returns
// to the ground state.
func (t *tokenizer) finish(tm term, emit func(token)) {
	if !t.discard {
		emit(token{kind: t.sk, raw: t.seq, term: tm})
	}
	t.reset()
}

// cut emits the sequence as cut, unless it was cut at its cap already,
// and returns to the ground state.
func (t *tokenizer) cut(emit func(token)) {
	if !t.discard {
		emit(token{kind: t.sk, raw: t.seq, cut: true})
	}
	t.reset()
}

func (t *tokenizer) reset() {
	t.seq = t.seq[:0]
	t.discard = false
	t.st = stGround
}

func (t *tokenizer) flushText(emit func(token)) {
	if len(t.text) == 0 {
		return
	}
	emit(token{kind: kText, raw: t.text})
	t.text = t.text[:0]
}

// byteValues holds every byte value, so a one-byte token needs no
// allocation.
var byteValues = func() (a [256]byte) {
	for i := range a {
		a[i] = byte(i)
	}
	return a
}()
