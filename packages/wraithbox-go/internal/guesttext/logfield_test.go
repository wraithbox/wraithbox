package guesttext

import (
	"strings"
	"testing"
)

// TestLogField checks the escapes, the cut and its marker
// (S07-02-packet-path, "Events and rate limits").
func TestLogField(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{
		{"plain", "mac-mini", 64, `"mac-mini"`},
		{"empty", "", 64, `""`},
		{"control bytes", "a\x00b\x1b[31m\r\n\x7f", 64, `"a\x00b\x1b[31m\x0d\x0a\x7f"`},
		{"quote and backslash", `a"b\c`, 64, `"a\"b\\c"`},
		{"non-ASCII", "café", 64, `"caf\xc3\xa9"`},
		{"invalid UTF-8", "a\x9bb", 64, `"a\x9bb"`},
		{"exactly the maximum", "abcd", 4, `"abcd"`},
		{"cut", "abcdef", 4, `"abcd"[cut from 6 bytes]`},
		{"cut inside a rune", "aé", 2, `"a\xc3"[cut from 3 bytes]`},
		{"maximum zero", "abc", 0, `""[cut from 3 bytes]`},
		{"maximum negative", "abc", -1, `""[cut from 3 bytes]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LogField(tt.in, tt.maxBytes); got != tt.want {
				t.Errorf("LogField(%q, %d) = %s, want %s", tt.in, tt.maxBytes, got, tt.want)
			}
		})
	}
}

// FuzzLogField checks that the result is printable ASCII, bounded in
// length, and marked when cut, for any string.
func FuzzLogField(f *testing.F) {
	f.Add("mac-mini", 64)
	f.Add("a\x00\x1b\xff\"\\", 3)
	f.Fuzz(func(t *testing.T, s string, maxBytes int) {
		maxBytes %= 1024
		got := LogField(s, maxBytes)
		for i := range len(got) {
			if c := got[i]; c < 0x20 || c >= 0x7f {
				t.Fatalf("LogField(%q, %d) = %q holds byte %#x", s, maxBytes, got, c)
			}
		}
		kept := max(maxBytes, 0)
		if limit := 4*kept + 2 + len("[cut from  bytes]") + 20; len(got) > limit {
			t.Fatalf("LogField(%q, %d) is %d bytes, over %d", s, maxBytes, len(got), limit)
		}
		if cut := len(s) > kept; cut != strings.HasSuffix(got, " bytes]") {
			t.Fatalf("LogField(%q, %d) = %q, cut %v", s, maxBytes, got, cut)
		}
	})
}
