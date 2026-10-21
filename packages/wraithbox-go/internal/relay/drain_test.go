package relay

import (
	"regexp"
	"testing"
)

// reply is the form the drain ends on (S16-03-around-the-stream, "Input
// drain"), the reference FuzzDrainReply checks the scanner against.
var reply = regexp.MustCompile(`\x1b\[\?[0-9;]{1,32}R`)

// drainEnd feeds in to a new scanner in chunks cut at split, and
// returns the index just past the reply in in, or -1.
func drainEnd(in []byte, split int) int {
	var d drainScanner
	base := 0
	for len(in) > 0 {
		n := len(in)
		if split > 0 && split < n {
			n = split
		}
		i, ok := d.feed(in[:n])
		if i > n {
			return -2
		}
		if ok {
			return base + i
		}
		base += n
		in = in[n:]
	}
	return -1
}

func TestDrainScanner(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"\x1b[?1;1R", 7},
		{"abc\x1b[?24;80;1R rest", 14},
		{"\x1b[1;1R", -1},
		{"\x1b[24;80R\x1b[?1;1R", 15},
		{"\x1b[?R", -1},
		{"\x1b[?1;2$y", -1},
		{"\x1b[?1u\x1b[?62;22c", -1},
		{"\x1b[?\x1b[?5R", 8},
		{"\x1b[?123456789012345678901234567890123R", -1},
		{"\x1b[?12345678901234567890123456789012R", 36},
		{"\x1b\x1b[?1R", 6},
	}
	for _, tt := range tests {
		for _, split := range []int{0, 1, 2, 3} {
			if got := drainEnd([]byte(tt.in), split); got != tt.want {
				t.Errorf("drainEnd(%q, split %d) = %d, want %d", tt.in, split, got, tt.want)
			}
		}
	}
}

// FuzzDrainReply: whatever the terminal input, which replies to guest
// queries influence, the drain ends exactly where the first reply of
// the form `CSI ? … R` ends, wherever the reads split the input. So it
// never ends early on a reply without `?`, never reads past the reply,
// and never panics.
func FuzzDrainReply(f *testing.F) {
	for _, s := range []string{
		"\x1b[?1;1R", "\x1b[1;1R", "typed\x1b[2;3R\x1b[?2;3R", "\x1b[?62;22c\x1b[?1;2$y",
		"\x1b[?\x1b[?1R", "\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[?5R",
	} {
		f.Add([]byte(s), uint8(0))
	}
	f.Fuzz(func(t *testing.T, in []byte, split uint8) {
		want := -1
		if loc := reply.FindIndex(in); loc != nil {
			want = loc[1]
		}
		if got := drainEnd(in, int(split)); got != want {
			t.Fatalf("drain of %q split at %d ends at %d, want %d", in, split, got, want)
		}
	})
}
