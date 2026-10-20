package hostgit

import (
	"strings"
	"testing"
)

// TestParseVersion is the table of S08-08-host-git, "Where it is
// checked". The Windows suffix runs on every OS.
func TestParseVersion(t *testing.T) {
	type outcome int
	const (
		accepted outcome = iota
		tooOld
		unreadableOut
	)
	tests := []struct {
		name string
		out  string
		want outcome
	}{
		{"plain", "git version 2.54.0\n", accepted},
		{"apple", "git version 2.54.0 (Apple Git-157)\n", accepted},
		{"windows", "git version 2.54.0.windows.1\n", accepted},
		{"crlf", "git version 2.54.0\r\n", accepted},
		{"no newline", "git version 2.54.0", accepted},
		{"second line ignored", "git version 2.54.0\ngarbage\x00\n", accepted},
		{"newer", "git version 3.0.0\n", accepted},
		{"newer minor", "git version 2.100.0\n", accepted},
		{"too old", "git version 2.53.9\n", tooOld},
		{"too old major", "git version 1.99.99\n", tooOld},
		{"release candidate", "git version 2.54.0.rc1\n", unreadableOut},
		{"two numbers", "git version 2.54\n", unreadableOut},
		{"dash suffix", "git version 2.54.0-foo\n", unreadableOut},
		{"version alone", "git version\n", unreadableOut},
		{"version and space", "git version \n", unreadableOut},
		{"leading text", "warning: x\ngit version 2.54.0\n", unreadableOut},
		{"leading space", " git version 2.54.0\n", unreadableOut},
		{"empty", "", unreadableOut},
		{"overlong number", "git version 2.5400000000000000000000.0\n", unreadableOut},
		{"seven digits", "git version 2.1000000.0\n", unreadableOut},
		{"empty comment", "git version 2.54.0 \n", unreadableOut},
		{"control in comment", "git version 2.54.0 (a\x1bb)\n", unreadableOut},
		{"windows without number", "git version 2.54.0.windows.\n", unreadableOut},
		{"windows and more", "git version 2.54.0.windows.1 (x)\n", unreadableOut},
		{"sign", "git version +2.54.0\n", unreadableOut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, ok := firstLine([]byte(tt.out), false)
			v, readable := parseVersionLine(line)
			got := accepted
			switch {
			case !ok || !readable:
				got = unreadableOut
			case v.less(minimum):
				got = tooOld
			}
			if got != tt.want {
				t.Errorf("%q: outcome %d, want %d", tt.out, got, tt.want)
			}
		})
	}
}

func TestFirstLineTruncated(t *testing.T) {
	if _, ok := firstLine([]byte("git version 2.54.0"), true); ok {
		t.Error("a line cut at the bound must not be readable")
	}
	if line, ok := firstLine([]byte("git version 2.54.0\nmore"), true); !ok || line != "git version 2.54.0" {
		t.Errorf("firstLine = %q, %v", line, ok)
	}
}

func TestMinimum(t *testing.T) {
	if minimum != (version{major: 2, minor: 54, patch: 0}) {
		t.Errorf("minimum = %+v, want 2.54.0", minimum)
	}
}

func TestCappedBuffer(t *testing.T) {
	c := NewCappedBuffer(4)
	for _, p := range []string{"12", "3456", "78"} {
		n, err := c.Write([]byte(p))
		if n != len(p) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}
	if string(c.Bytes()) != "1234" || !c.Over() {
		t.Errorf("buf = %q, over = %v; want %q, true", c.Bytes(), c.Over(), "1234")
	}
}

// FuzzParseVersion: the parser never panics, and what it accepts has the
// prefix and three bounded numbers.
func FuzzParseVersion(f *testing.F) {
	for _, s := range []string{
		"git version 2.54.0", "git version 2.54.0 (Apple Git-157)",
		"git version 2.54.0.windows.1", "git version 2.54.0.rc1", "git version",
		"git version 999999.999999.999999",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		v, ok := parseVersionLine(line)
		if !ok {
			return
		}
		if !strings.HasPrefix(line, "git version ") {
			t.Fatalf("accepted %q without the prefix", line)
		}
		for _, n := range []int{v.major, v.minor, v.patch} {
			if n < 0 || n > 999999 {
				t.Fatalf("accepted %q with number %d", line, n)
			}
		}
	})
}
