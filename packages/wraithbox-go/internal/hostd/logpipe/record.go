package logpipe

import (
	"bytes"
	"strconv"
	"time"
	"unicode/utf8"
)

// splitter cuts a byte stream into lines of at most MaxLine bytes. It
// holds one line, in a buffer of MaxLine bytes that never grows. A line
// longer than that is passed on cut at MaxLine, and the rest of it up to
// the next newline is discarded.
type splitter struct {
	line       []byte
	discarding bool
}

func newSplitter() splitter {
	return splitter{line: make([]byte, 0, MaxLine)}
}

// feed splits p, and calls emit for each line it completes or cuts. The
// slice passed to emit is reused after emit returns.
func (s *splitter) feed(p []byte, emit func(line []byte, cut bool)) {
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			s.add(p, emit)
			return
		}
		s.add(p[:i], emit)
		if !s.discarding {
			emit(s.line, false)
		}
		s.line = s.line[:0]
		s.discarding = false
		p = p[i+1:]
	}
}

func (s *splitter) add(chunk []byte, emit func([]byte, bool)) {
	if s.discarding {
		return
	}
	room := MaxLine - len(s.line)
	if len(chunk) <= room {
		s.line = append(s.line, chunk...)
		return
	}
	s.line = append(s.line, chunk[:room]...)
	emit(s.line, true)
	s.line = s.line[:0]
	s.discarding = true
}

// end passes on a last line without a newline.
func (s *splitter) end(emit func([]byte, bool)) {
	if len(s.line) > 0 {
		emit(s.line, false)
	}
	s.line = s.line[:0]
	s.discarding = false
}

// appendRecord appends e as one JSON object and a newline. The process
// name has the fixed form of checkProcess, and the rules are constants,
// so only the line needs escaping. A record is all printable ASCII.
//
// A line record: {"time":…,"process":…,"line":…}, with "truncated":true
// when the line was cut at MaxLine. A drop record:
// {"time":…,"process":…,"rule":…,"dropped":N}.
func appendRecord(dst []byte, process string, e entry) []byte {
	dst = append(dst, `{"time":"`...)
	dst = e.at.UTC().AppendFormat(dst, time.RFC3339Nano)
	dst = append(dst, `","process":"`...)
	dst = append(dst, process...)
	if e.rule != "" {
		dst = append(dst, `","rule":"`...)
		dst = append(dst, e.rule...)
		dst = append(dst, `","dropped":`...)
		dst = strconv.AppendInt(dst, e.dropped, 10)
		return append(dst, "}\n"...)
	}
	dst = append(dst, `","line":`...)
	dst = appendString(dst, e.line)
	if e.cut {
		dst = append(dst, `,"truncated":true`...)
	}
	return append(dst, "}\n"...)
}

const hexDigits = "0123456789abcdef"

// appendString appends b as a JSON string of printable ASCII. A control
// byte, DEL and every non-ASCII character is a \u escape, with a
// surrogate pair above U+FFFF. A byte that isn't valid UTF-8 is U+FFFD.
// So the file holds nothing a terminal or an editor interprets, and each
// byte of b is at most one character of the decoded string.
func appendString(dst, b []byte) []byte {
	dst = append(dst, '"')
	for len(b) > 0 {
		c := b[0]
		if c >= 0x20 && c < 0x7f {
			if c == '"' || c == '\\' {
				dst = append(dst, '\\')
			}
			dst = append(dst, c)
			b = b[1:]
			continue
		}
		r, size := rune(c), 1
		if c >= utf8.RuneSelf {
			r, size = utf8.DecodeRune(b)
		}
		dst = appendEscape(dst, r)
		b = b[size:]
	}
	return append(dst, '"')
}

func appendEscape(dst []byte, r rune) []byte {
	if r > 0xffff {
		r -= 0x10000
		dst = appendU(dst, 0xd800+(r>>10))
		return appendU(dst, 0xdc00+(r&0x3ff))
	}
	return appendU(dst, r)
}

func appendU(dst []byte, r rune) []byte {
	return append(dst, '\\', 'u',
		hexDigits[r>>12&0xf], hexDigits[r>>8&0xf], hexDigits[r>>4&0xf], hexDigits[r&0xf])
}
