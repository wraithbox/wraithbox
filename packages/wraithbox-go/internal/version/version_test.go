package version

import (
	"errors"
	"strings"
	"testing"
)

// withStamp sets the link-time stamp for one test, the way the build
// tasks' -X flag does.
func withStamp(t *testing.T, s string) {
	t.Helper()
	old := stamp
	stamp = s
	t.Cleanup(func() { stamp = old })
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name  string
		stamp string
		want  string
	}{
		{name: "unstamped", stamp: "", want: ""},
		{name: "clean tree", stamp: "0.1.0+g0123456789abcdef", want: "0.1.0+g0123456789abcdef"},
		{name: "modified tree", stamp: "0.1.0+g0123456789abcdef.dirty.fedcba9876543210", want: "0.1.0+g0123456789abcdef.dirty.fedcba9876543210"},
		{name: "stamp outside the form is no version", stamp: "0.1.0 +g01", want: ""},
		{name: "old default is no version", stamp: "dev", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withStamp(t, tt.stamp)
			if got := Build(); got != tt.want {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		name   string
		stamp  string
		binary string
		want   string
	}{
		{name: "unstamped cli", binary: "wb", want: "wb (Wraith Box) unstamped"},
		{name: "unstamped gateway", binary: "wb-netd", want: "wb-netd (Wraith Box) unstamped"},
		{name: "stamped", stamp: "1.2.3+g01ab", binary: "wb-hostd", want: "wb-hostd (Wraith Box) 1.2.3+g01ab"},
		{name: "malformed stamp", stamp: "1.2.3\n", binary: "wb", want: "wb (Wraith Box) unstamped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withStamp(t, tt.stamp)
			if got := String(tt.binary); got != tt.want {
				t.Errorf("String(%q) = %q, want %q", tt.binary, got, tt.want)
			}
		})
	}
}

func TestValid(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1.2.3", true},
		{"0.0.0", true},
		{"10.200.3000", true},
		{"1.2.3-rc.1", true},
		{"1.2.3+g1a2b3c4d", true},
		{"1.2.3+g1a2b3c4d.dirty.0011aabb", true},
		{"1.2.3-beta-2+build.7", true},
		{"1.2.3+a-b-c", true},
		{"", false},
		{"1.2", false},
		{"1.2.3.4", false},
		{"v1.2.3", false},
		{"1..3", false},
		{"1.2.x", false},
		{"1.2.3-", false},
		{"1.2.3+", false},
		{"1.2.3-+b", false},
		{"1.2.3+b+c", false},
		{"1.2.3+b-c+d", false},
		{"1.2.3+b_c", false},
		{"1.2.3+b c", false},
		{"1.2.3+b\x00", false},
		{"1.2.3\n", false},
		{"1.2.3+café", false},
		{"0-dev", false},
		{"1.2.3+" + strings.Repeat("a", MaxLen-6), true},
		{"1.2.3+" + strings.Repeat("a", MaxLen-5), false},
	}
	for _, tt := range tests {
		if got := Valid(tt.in); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCheckMatches(t *testing.T) {
	for _, v := range []string{"0.1.0+g0123456789abcdef", "0.1.0+g0123456789abcdef.dirty.00ff", "1.2.3"} {
		if err := Check(v, v); err != nil {
			t.Errorf("Check(%q, %q) = %v, want a match", v, v, err)
		}
	}
}

func TestCheckRefuses(t *testing.T) {
	const v = "0.1.0+g0123456789abcdef"
	tests := []struct {
		name     string
		own      string
		peer     string
		wantRule string
	}{
		{name: "two unstamped peers refuse each other", own: "", peer: "", wantRule: RuleUnstamped},
		{name: "unstamped peer", own: v, peer: "", wantRule: RuleUnstamped},
		{name: "unstamped self", own: "", peer: v, wantRule: RuleUnstamped},
		{name: "two equal versions outside the form", own: "dev", peer: "dev", wantRule: RuleUnstamped},
		{name: "peer outside the form", own: v, peer: v + "\n", wantRule: RuleUnstamped},
		{name: "another revision", own: v, peer: "0.1.0+g0123456789abcde0", wantRule: RuleMismatch},
		{name: "another modified tree", own: v + ".dirty.01", peer: v + ".dirty.02", wantRule: RuleMismatch},
		{name: "clean and modified tree", own: v, peer: v + ".dirty.01", wantRule: RuleMismatch},
		{name: "case differs", own: "1.2.3+gab", peer: "1.2.3+gAB", wantRule: RuleMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Check(tt.own, tt.peer)
			var r *Refusal
			if !errors.As(err, &r) {
				t.Fatalf("Check(%q, %q) = %v, want a *Refusal", tt.own, tt.peer, err)
			}
			if r.Rule != tt.wantRule || r.Own != tt.own || r.Peer != tt.peer {
				t.Errorf("Check(%q, %q) = %+v, want rule %s with both versions", tt.own, tt.peer, r, tt.wantRule)
			}
			if !strings.Contains(err.Error(), tt.wantRule) {
				t.Errorf("Error() = %q, want it to name the rule %s", err.Error(), tt.wantRule)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "unstamped"},
		{"1.2.3+gab", `"1.2.3+gab"`},
		{"1.2.3\x1b[31m", `"1.2.3\x1b[31m"`},
		{"\xff\n", `"\xff\n"`},
		{strings.Repeat("a", MaxLen+10), `"` + strings.Repeat("a", MaxLen) + `"`},
	}
	for _, tt := range tests {
		if got := Label(tt.in); got != tt.want {
			t.Errorf("Label(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// FuzzCheck feeds Check and Label the strings a peer could send as its
// version. A peer's version is adversarial input once wb-guestd reports
// it (S04-08-records-and-bounds, "The guest's version is a label").
func FuzzCheck(f *testing.F) {
	f.Add("0.1.0+g0123456789abcdef", "0.1.0+g0123456789abcdef")
	f.Add("", "")
	f.Add("1.2.3-rc.1+b", "1.2.3-rc.1+b\x00")
	f.Add("1.2.3", "\xff\xfe")
	f.Fuzz(func(t *testing.T, own, peer string) {
		err := Check(own, peer)
		if err == nil && (own != peer || !Valid(own)) {
			t.Fatalf("Check(%q, %q) matched", own, peer)
		}
		if err == nil {
			assertForm(t, peer)
		}
		label := Label(peer)
		if len(label) > 4*MaxLen+2 {
			t.Fatalf("Label(%q) is %d bytes", peer, len(label))
		}
		for i := range len(label) {
			if label[i] < 0x20 || label[i] > 0x7e {
				t.Fatalf("Label(%q) = %q holds byte %#x", peer, label, label[i])
			}
		}
	})
}

// assertForm fails unless s is printable ASCII of at most MaxLen bytes.
func assertForm(t *testing.T, s string) {
	t.Helper()
	if len(s) == 0 || len(s) > MaxLen {
		t.Fatalf("%q matched with %d bytes", s, len(s))
	}
	for i := range len(s) {
		if s[i] <= 0x20 || s[i] > 0x7e {
			t.Fatalf("%q matched with byte %#x", s, s[i])
		}
	}
}
