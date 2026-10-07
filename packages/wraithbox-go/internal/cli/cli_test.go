package cli

import (
	"errors"
	"slices"
	"testing"
)

// parseCase is one row of TestParse: the arguments and what Parse
// should make of them.
type parseCase struct {
	name    string
	args    []string
	command string
	rest    []string
	globals Globals
}

func (tc parseCase) check(t *testing.T) {
	t.Helper()
	inv, err := Parse(tc.args)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", tc.args, err)
	}
	if inv.Globals != tc.globals {
		t.Errorf("globals = %+v, want %+v", inv.Globals, tc.globals)
	}
	got := ""
	if inv.Command != nil {
		got = inv.Command.Name
	}
	if got != tc.command {
		t.Errorf("command = %q, want %q", got, tc.command)
	}
	if tc.command != "" && !slices.Equal(inv.Args, tc.rest) {
		t.Errorf("args = %q, want %q", inv.Args, tc.rest)
	}
}

func TestParse(t *testing.T) {
	tests := []parseCase{
		{name: "no args", args: nil},
		{name: "claude bare", args: []string{"claude"}, command: "claude", rest: []string{}},
		{
			name:    "claude args pass through",
			args:    []string{"claude", "--resume", "-p", "hi"},
			command: "claude", rest: []string{"--resume", "-p", "hi"},
		},
		{
			name:    "flags after claude belong to claude",
			args:    []string{"claude", "--isolated", "--help", "--", "x"},
			command: "claude", rest: []string{"--isolated", "--help", "--", "x"},
		},
		{
			name:    "global flags before claude",
			args:    []string{"--isolated", "--ephemeral", "-C", "/src/p", "claude", "mcp", "list"},
			command: "claude", rest: []string{"mcp", "list"},
			globals: Globals{Isolated: true, Ephemeral: true, Dir: "/src/p"},
		},
		{
			name:    "management command keeps its args",
			args:    []string{"land", "s-1", "--branch", "x"},
			command: "land", rest: []string{"s-1", "--branch", "x"},
		},
		{
			name:    "guest flag",
			args:    []string{"--guest", "linux", "claude"},
			command: "claude", rest: []string{},
			globals: Globals{Guest: "linux"},
		},
		{name: "help flag", args: []string{"--help"}, globals: Globals{Help: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"bogus"}},
		{name: "unknown global flag", args: []string{"--resume", "claude"}},
		{name: "session flag on management command", args: []string{"--isolated", "status"}},
		{name: "learn on management command", args: []string{"--learn", "land", "s-1"}},
		{name: "guest on management command", args: []string{"--guest", "linux", "status"}},
		{name: "guest without value", args: []string{"--guest"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args)
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("Parse(%q) error = %v, want ErrUsage", tt.args, err)
			}
		})
	}
}

func TestCommandNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Commands {
		if seen[c.Name] {
			t.Errorf("duplicate command %q", c.Name)
		}
		seen[c.Name] = true
	}
}
