package termsetup

import (
	"strings"
	"testing"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

func TestValidSize(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup *terminalv1.Setup
		want  bool
	}{
		{"nil", nil, false},
		{"piped without size", &terminalv1.Setup{}, true},
		{"interactive without size", &terminalv1.Setup{Interactive: true}, false},
		{"interactive", &terminalv1.Setup{Interactive: true, Size: &terminalv1.WindowSize{Rows: 24, Cols: 80}}, true},
		{"rows 0", &terminalv1.Setup{Interactive: true, Size: &terminalv1.WindowSize{Rows: 0, Cols: 80}}, false},
		{"cols 65536", &terminalv1.Setup{Interactive: true, Size: &terminalv1.WindowSize{Rows: 24, Cols: 65536}}, false},
		{"at the top", &terminalv1.Setup{Interactive: true, Size: &terminalv1.WindowSize{Rows: 65535, Cols: 65535}}, true},
	} {
		if got := ValidSize(tc.setup); got != tc.want {
			t.Errorf("%s: ValidSize = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidEnv(t *testing.T) {
	env := func(kv ...string) []*terminalv1.EnvVar {
		var out []*terminalv1.EnvVar
		for i := 0; i < len(kv); i += 2 {
			out = append(out, &terminalv1.EnvVar{Name: kv[i], Value: kv[i+1]})
		}
		return out
	}
	for _, tc := range []struct {
		name string
		env  []*terminalv1.EnvVar
		want bool
	}{
		{"none", nil, true},
		{"each name", env("TERM", "xterm", "TERM_PROGRAM", "a", "TERM_PROGRAM_VERSION", "1", "COLORTERM", "truecolor",
			"LANG", "en_US.UTF-8", "LC_ALL", "C", "LC_CTYPE", "UTF-8"), true},
		{"other name", env("PATH", "/bin"), false},
		{"name twice", env("TERM", "a", "TERM", "b"), false},
		{"escape", env("TERM", "x\x1b"), false},
		{"newline", env("TERM", "x\n"), false},
		{"non-ASCII", env("LANG", "é"), false},
		{"at the cap", env("TERM", strings.Repeat("a", MaxEnvValue)), true},
		{"over the cap", env("TERM", strings.Repeat("a", MaxEnvValue+1)), false},
	} {
		if got := ValidEnv(tc.env); got != tc.want {
			t.Errorf("%s: ValidEnv = %v, want %v", tc.name, got, tc.want)
		}
	}
}
