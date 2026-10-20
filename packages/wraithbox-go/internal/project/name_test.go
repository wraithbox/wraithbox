package project

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, name := range []string{"foo", "Foo", "a", "wraithbox-go", "v1.2_x", strings.Repeat("a", MaxNameLen)} {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{
		"", strings.Repeat("a", MaxNameLen+1), "-x", ".x", "_x", "a..b", "a b", "a/b", "a:b", "a~b", "a^b", "a@{b",
		"a\\b", "a*b", "a?b", "a[b", "é", "a\x1bb", "a\x00",
	} {
		if err := ValidName(name); err == nil {
			t.Errorf("ValidName(%q) = nil, want an error", name)
		}
	}
}

func TestNameFrom(t *testing.T) {
	for in, want := range map[string]string{
		"foo":                          "foo",
		"My Project":                   "My-Project",
		".dotfiles":                    "dotfiles",
		"--x":                          "x",
		"a..b":                         "a.-b",
		"a...b":                        "a.-.b",
		"café":                         "caf-",
		"":                             fallbackName,
		"...":                          fallbackName,
		"日本":                           fallbackName,
		strings.Repeat("a", 100):       strings.Repeat("a", MaxNameLen),
		"x\x1b[0m":                     "x--0m",
		"wb/" + strings.Repeat("b", 3): "wb-bbb",
	} {
		if got := nameFrom(in); got != want {
			t.Errorf("nameFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDefaultName: the name comes from the directory that holds the
// common directory, a submodule's from its own directory, and a bare
// common directory's from its own name (S05-04-naming, "Name").
func TestDefaultName(t *testing.T) {
	root := string(filepath.Separator)
	p := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	tests := []struct {
		kind      Kind
		d, common string
		want      string
	}{
		{KindMain, p("src", "foo"), p("src", "foo", ".git"), "foo"},
		{KindWorktree, p("src", "foo-wt"), p("src", "foo", ".git"), "foo"},
		{KindWorktree, p("src", "wt"), p("src", "tool.git"), "tool"},
		{KindSubmodule, p("src", "foo", "lib", "sub"), p("src", "foo", ".git", "modules", "lib", "sub"), "sub"},
	}
	for _, tt := range tests {
		if got := defaultName(tt.kind, tt.d, tt.common); got != tt.want {
			t.Errorf("defaultName(%s, %s, %s) = %q, want %q", tt.kind, tt.d, tt.common, got, tt.want)
		}
	}
}
