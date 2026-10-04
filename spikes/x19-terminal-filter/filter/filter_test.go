package filter

import (
	"bytes"
	"strings"
	"testing"
)

func TestFilter(t *testing.T) {
	tests := []struct {
		name, in, want, rule string
	}{
		{"text", "hello", "hello", ""},
		{"sgr", "\x1b[38;2;1;2;3mx\x1b[0m", "\x1b[38;2;1;2;3mx\x1b[0m", ""},
		{"sync output", "\x1b[?2026hx\x1b[?2026l", "\x1b[?2026hx\x1b[?2026l", ""},
		{"osc52 clipboard write", "a\x1b]52;c;aGVsbG8=\x07b", "ab", "osc52-clipboard"},
		{"osc52 st", "a\x1b]52;c;aGVsbG8=\x1b\\b", "ab", "osc52-clipboard"},
		{"osc52 clipboard read", "\x1b]52;c;?\x07", "", "osc52-clipboard"},
		{"iterm2 file transfer", "\x1b]1337;File=name=eA==;size=1:eA==\x07", "", "osc1337-iterm2"},
		{"iterm2 notification", "\x1b]9;wb approve: allow evil.example?\x07", "\x07", "osc-notification-to-bel"},
		{"ghostty notification", "\x1b]777;notify;wb;approve?\x07", "\x07", "osc-notification-to-bel"},
		{"kitty notification", "\x1b]99;i=1:d=0;t\x1b\\", "\x07", "osc-notification-to-bel"},
		{"progress", "\x1b]9;4;1;50\x07", "\x1b]9;4;1;50\x07", ""},
		{"dcs tmux passthrough", "\x1bPtmux;\x1b\x1b]52;c;eA==\x07\x1b\\x", "x", "osc52-clipboard"},
		{"dcs xtgettcap", "\x1bP+q544e\x1b\\", "", "dcs"},
		{"kitty graphics file read", "\x1b_Ga=T,t=f;L2V0Yy9wYXNzd2Q=\x1b\\", "", "apc"},
		{"pm", "\x1b^secret\x1b\\", "", "sos-pm"},
		{"osc8 https", "\x1b]8;;https://example.com\x07x\x1b]8;;\x07", "\x1b]8;;https://example.com\x07x\x1b]8;;\x07", ""},
		{"osc8 file", "\x1b]8;;file:///Applications/Calculator.app\x07x\x1b]8;;\x07", "x\x1b]8;;\x07", "osc8-scheme"},
		{"osc8 custom scheme", "\x1b]8;;vscode://file/etc/passwd\x07x", "x", "osc8-scheme"},
		{"osc7 cwd", "\x1b]7;file://host/etc\x07", "", "osc7-cwd"},
		{"title", "\x1b]0;Claude Code\x07", "\x1b]0;Claude Code\x07", ""},
		{"title with c1", "\x1b]0;a\u009b31mb\x07", "", "osc-title-unclean"},
		{"osc color set", "\x1b]11;#ff0000\x07", "", "osc-color-set"},
		{"osc color query", "\x1b]11;?\x07", "\x1b]11;?\x07", ""},
		{"window resize", "\x1b[8;100;100t", "", "csi-window-ops"},
		{"title report", "\x1b[21t", "", "csi-window-ops"},
		{"unknown private mode", "\x1b[?9999h", "", "csi-mode-not-allowed"},
		{"mode list with one bad", "\x1b[?25;9999h", "", "csi-mode-not-allowed"},
		{"csi bad params", "\x1b[1<2mX", "X", "csi-bad-params"},
		{"enq answerback", "a\x05b", "ab", "c0-not-allowed"},
		{"shift out", "\x0e", "", "c0-not-allowed"},
		{"c1 csi as utf8", "a\u009b31mb", "a31mb", "c1-control"},
		{"raw 8-bit csi", "a\x9b31mb", "a�31mb", "invalid-utf8-replaced"},
		{"overlong utf8", "\xc0\xafx", "��x", "invalid-utf8-replaced"},
		{"esc reset", "\x1bc", "", "esc-not-allowed"},
		{"csi over length", "\x1b[" + strings.Repeat("1;", 40) + "mX", "X", "over-length"},
		{"osc over length", "\x1b]0;" + strings.Repeat("a", maxStr+10) + "\x07X", "X", "over-length"},
		{"unterminated osc then text", "\x1b]52;c;eA==\x18X", "X", "malformed"},
		{"esc ends osc", "\x1b]52;c;eA==\x1b[1mX", "\x1b[1mX", "malformed"},
		{"c0 inside csi", "\x1b[1\n2mX", "\n2mX", "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			f := New(&out)
			var logged []string
			f.OnDrop = func(tok Token, d Decision) { logged = append(logged, d.Rule) }
			f.Write([]byte(tt.in))
			if got := out.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if tt.rule != "" && !contains(logged, tt.rule) {
				t.Errorf("rule %q not logged, got %v", tt.rule, logged)
			}
			if tt.rule == "" && len(logged) > 0 {
				t.Errorf("unexpected drops %v", logged)
			}
		})
	}
}

// A sequence split at every byte boundary gives the same result.
func TestSplit(t *testing.T) {
	in := "a\x1b]52;c;eA==\x07\x1b[31mé\x1b]8;;https://x.example\x1b\\b\x1b]8;;\x1b\\\U0001F600"
	want := string(Apply([]byte(in)))
	for i := 0; i <= len(in); i++ {
		var out bytes.Buffer
		f := New(&out)
		f.Write([]byte(in[:i]))
		f.Write([]byte(in[i:]))
		if out.String() != want {
			t.Fatalf("split at %d: got %q, want %q", i, out.String(), want)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
