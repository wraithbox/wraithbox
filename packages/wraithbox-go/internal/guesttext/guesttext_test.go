package guesttext

import (
	"testing"
	"unicode/utf8"
)

func TestStrip(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "myrepo-20261021-1412-k3f9", "myrepo-20261021-1412-k3f9"},
		{"unicode text stays", "café 日本", "café 日本"},
		{"ESC and a sequence's introducer", "a\x1b]2;x\x07b", "a]2;xb"},
		{"C1 as a code point", "a\u009b31mb", "a31mb"},
		{"lone 8-bit CSI byte", "a\x9b31mb", "a31mb"},
		{"BEL, CR, LF, DEL", "a\a\r\n\x7fb", "ab"},
		{"bidirectional override", "a\u202eb", "ab"},
		{"zero-width joiner and space", "a\u200d\u200bb", "ab"},
		{"line and paragraph separators", "a\u2028\u2029b", "ab"},
		{"tag characters", "a\U000E0041b", "ab"},
		{"variation selector", "a\ufe0fb", "ab"},
		{"Hangul filler", "a\u3164b", "ab"},
		{"BOM", "\xef\xbb\xbf" + "ab", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Strip(tt.in); got != tt.want {
				t.Errorf("Strip(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// FuzzStrip: whatever the guest sent, Strip's result is valid UTF-8
// with no code point of the escaped set, and stripping again changes
// nothing.
func FuzzStrip(f *testing.F) {
	for _, s := range []string{"", "a\x1b[31mb", "\xff\xfe", "\u202e\u2028", "\U000E0041"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := Strip(in)
		if !utf8.ValidString(out) {
			t.Fatalf("Strip(%q) = %q, not valid UTF-8", in, out)
		}
		for _, r := range out {
			if Escaped(r) {
				t.Fatalf("Strip(%q) = %q, holds %U", in, out, r)
			}
		}
		if again := Strip(out); again != out {
			t.Fatalf("Strip twice = %q, once = %q", again, out)
		}
	})
}
