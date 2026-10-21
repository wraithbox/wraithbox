// Package guesttext makes text that the guest influenced safe to print
// outside the terminal stream (S16-03-around-the-stream, "Guest text
// elsewhere"): a session or shell id in the window title, a link in the
// summary, a failure message. Such text never reaches the terminal with
// a code point of the escaped set, so it can't move the cursor, hide
// text or start an escape sequence (SEC03-no-host-exec,
// SEC14-no-fake-approvals).
package guesttext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// escaped is the escaped set of S16-03-around-the-stream: controls (Cc,
// which holds ESC and the C1 controls), format characters (Cf), line
// and paragraph separators (Zl, Zp), and the Default_Ignorable_Code_Point
// property. Go has no table for that derived property. It is
// Other_Default_Ignorable_Code_Point, Cf and Variation_Selector less a
// few code points, so the union below holds all of it and a few more.
var escaped = []*unicode.RangeTable{
	unicode.Cc,
	unicode.Cf,
	unicode.Zl,
	unicode.Zp,
	unicode.Other_Default_Ignorable_Code_Point,
	unicode.Variation_Selector,
}

// Escaped reports whether r is in the escaped set.
func Escaped(r rune) bool {
	return unicode.IsOneOf(escaped, r)
}

// Strip returns s without the code points of the escaped set, and
// without the bytes that aren't valid UTF-8: a lone byte such as 0x9B
// is a C1 control to a terminal that reads 8-bit bytes. The result is
// valid UTF-8.
func Strip(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r != utf8.RuneError || n > 1 {
			if !Escaped(r) {
				b.WriteString(s[:n])
			}
		}
		s = s[n:]
	}
	return b.String()
}
