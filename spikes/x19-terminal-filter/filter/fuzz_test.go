package filter

import (
	"bytes"
	"testing"
)

// FuzzFilter checks the properties the relay relies on: the output holds
// only allowed tokens, filtering is idempotent, and where a read splits
// the stream does not change the result.
func FuzzFilter(f *testing.F) {
	for _, s := range []string{
		"hello\x1b[31mred\x1b[0m",
		"\x1b]52;c;aGVsbG8=\x07",
		"\x1b]8;;https://example.com\x1b\\x\x1b]8;;\x1b\\",
		"\x1bPtmux;\x1b\x1b]52;c;eA==\x07\x1b\\",
		"\x1b_Ga=T,t=f;eA==\x1b\\",
		"\x1b[?2026h\x1b[?1049h\x1b[>5u\x1b[?u",
		"\xc2\x9b31m\x9b\xe2\x82",
		"\x1b]0;title\x07\x1b]777;notify;a;b\x07",
	} {
		f.Add([]byte(s), uint16(3))
	}
	f.Fuzz(func(t *testing.T, in []byte, split uint16) {
		out := Apply(in)

		var tok Tokenizer
		tok.Feed(out, func(tk Token) {
			d := Decide(tk)
			if !d.Pass || d.Rewrite != nil && tk.Kind != Control {
				t.Fatalf("output token %s %q not allowed as is (rule %s)", tk.Kind, tk.Raw, d.Rule)
			}
		})
		if tok.Pending() != 0 {
			t.Fatalf("output ends in an incomplete sequence %q", out)
		}
		if again := Apply(out); !bytes.Equal(again, out) {
			t.Fatalf("not idempotent: %q -> %q -> %q", in, out, again)
		}
		for _, bad := range [][]byte{[]byte("\x1b]52"), []byte("\x1bP"), []byte("\x1b_"), []byte("\x1b]1337")} {
			if bytes.Contains(out, bad) {
				t.Fatalf("output contains %q: %q", bad, out)
			}
		}

		i := int(split) % (len(in) + 1)
		var b bytes.Buffer
		fl := New(&b)
		fl.Write(in[:i])
		fl.Write(in[i:])
		if !bytes.Equal(b.Bytes(), out) {
			t.Fatalf("split at %d changes output: %q vs %q", i, b.Bytes(), out)
		}
	})
}
