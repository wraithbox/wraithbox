package sessionproc

import (
	"bytes"
	"testing"
)

func TestParseStat(t *testing.T) {
	// Fields 7 to 21 of proc(5), then starttime, field 22, then one more.
	tail := " 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 777 19\n"
	cases := []struct {
		name string
		in   string
		want statLine
		ok   bool
	}{
		{"plain", "42 (zsh) S 1 42 42" + tail, statLine{"zsh", 'S', 42, 777}, true},
		{"parens and spaces in the name", "42 (a) b (c) T 1 42 9" + tail, statLine{"a) b (c", 'T', 9, 777}, true},
		{"no name", "42 zsh S 1 42 42" + tail, statLine{}, false},
		{"short", "42 (zsh) S 1 42 42 1 2\n", statLine{}, false},
		{"negative session", "42 (zsh) S 1 42 -1" + tail, statLine{}, false},
		{"long state", "42 (zsh) SS 1 42 42" + tail, statLine{}, false},
		{"over the bound", "42 (" + string(bytes.Repeat([]byte{'x'}, maxProcFile)) + ") S 1 42 42" + tail, statLine{}, false},
	}
	for _, c := range cases {
		got, err := parseStat([]byte(c.in))
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s: %+v (%v), want %+v ok=%v", c.name, got, err, c.want, c.ok)
		}
	}
}

func TestParseUIDs(t *testing.T) {
	cases := []struct {
		in         string
		ruid, euid uint32
		ok         bool
	}{
		{"Name:\tzsh\nUid:\t501\t502\t503\t504\nGid:\t20\t20\t20\t20\n", 501, 502, true},
		{"Name:\tzsh\n", 0, 0, false},
		{"Uid:\t501\t502\t503\n", 0, 0, false},
		{"Uid:\t501\t-1\t503\t504\n", 0, 0, false},
	}
	for _, c := range cases {
		r, e, err := parseUIDs([]byte(c.in))
		if (err == nil) != c.ok || r != c.ruid || e != c.euid {
			t.Errorf("%q: %d %d (%v), want %d %d ok=%v", c.in, r, e, err, c.ruid, c.euid, c.ok)
		}
	}
}

// FuzzParseStat feeds /proc/<pid>/stat parsing arbitrary bytes: a
// process names itself, so the line is guest input.
func FuzzParseStat(f *testing.F) {
	f.Add([]byte("42 (zsh) S 1 42 42 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 777 19\n"))
	f.Add([]byte("42 ()) S"))
	f.Fuzz(func(t *testing.T, b []byte) {
		st, err := parseStat(b)
		if err != nil {
			return
		}
		if st.session < 0 || st.start < 0 || len(st.name) > len(b) {
			t.Fatalf("out of range: %+v", st)
		}
	})
}

// FuzzParseUIDs feeds /proc/<pid>/status parsing arbitrary bytes.
func FuzzParseUIDs(f *testing.F) {
	f.Add([]byte("Uid:\t501\t502\t503\t504\n"))
	f.Add([]byte("Uid:"))
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _, _ = parseUIDs(b)
	})
}
