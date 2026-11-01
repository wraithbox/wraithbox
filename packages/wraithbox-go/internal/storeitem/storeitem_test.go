package storeitem

import (
	"errors"
	"strings"
	"testing"
)

// TestC05AccessGroups maps each item name form of S15-03-access-rules,
// "Declared access", to its role and its group,
// <TEAMID>.nl.wraithbox.<role> (C05-credentials-and-store, "Access
// groups").
func TestC05AccessGroups(t *testing.T) {
	const team = "ABCDE12345"
	tests := []struct {
		name  string
		form  Form
		role  Role
		group string
	}{
		{"wraithbox/binding/github-token", FormBinding, RoleBinding, "ABCDE12345.nl.wraithbox.binding"},
		{"wraithbox/ca/vm-1/ca-2", FormCA, RoleCA, "ABCDE12345.nl.wraithbox.ca"},
		{"wraithbox/wbadmin/build/b1", FormWbadminBuild, RoleWbadmin, "ABCDE12345.nl.wraithbox.wbadmin"},
		{"wraithbox/wbadmin/image/base", FormWbadminImage, RoleWbadmin, "ABCDE12345.nl.wraithbox.wbadmin"},
		{"wraithbox/wbadmin/vm/vm-1/disk-0", FormWbadminVM, RoleWbadmin, "ABCDE12345.nl.wraithbox.wbadmin"},
	}
	seen := map[Form]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := Parse(tt.name)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if n.Form() != tt.form || n.Role() != tt.role || n.String() != tt.name {
				t.Errorf("Parse = %q form %q role %q, want form %q role %q", n, n.Form(), n.Role(), tt.form, tt.role)
			}
			if g := Group(team, n.Role()); g != tt.group {
				t.Errorf("Group = %q, want %q", g, tt.group)
			}
		})
		seen[tt.form] = true
	}
	for _, f := range Forms() {
		if !seen[f] {
			t.Errorf("no case for the form %q", f)
		}
	}
}

// TestParseRefused feeds Parse names outside the forms of
// S15-03-access-rules, "Declared access", and sees each refused.
func TestParseRefused(t *testing.T) {
	long := strings.Repeat("a", MaxPart+1)
	for _, name := range []string{
		"",
		"wraithbox/",
		"binding/x",
		"Wraithbox/binding/x",
		"/wraithbox/binding/x",
		"wraithbox/binding/",
		"wraithbox/binding//x",
		"wraithbox/binding/" + long,
		"wraithbox/binding/X",
		"wraithbox/binding/a_b",
		"wraithbox/binding/a.b",
		"wraithbox/binding/..",
		"wraithbox/ca/vm/..",
		"wraithbox/binding/x/y",
		"wraithbox/ca/vm",
		"wraithbox/ca/vm/ca/extra",
		"wraithbox/ca//ca",
		"wraithbox/wbadmin/build",
		"wraithbox/wbadmin/vm/vm-1",
		"wraithbox/wbadmin/vm/vm-1/disk/extra",
		"wraithbox/wbadmin/other/x",
		"wraithbox/wbadmin/build/x/y",
		"wraithbox/secret/x",
		"wraithbox/Binding/x",
		"wraithbox/binding/x/",
		"wraithbox/binding/x\n",
	} {
		if n, err := Parse(name); err == nil {
			t.Errorf("Parse(%q) = %q, want an error", name, n)
		} else if !errors.Is(err, ErrName) {
			t.Errorf("Parse(%q): %v, want ErrName", name, err)
		}
	}
}

// TestParsePartLength accepts a part of MaxPart characters.
func TestParsePartLength(t *testing.T) {
	name := "wraithbox/binding/" + strings.Repeat("a", MaxPart)
	if _, err := Parse(name); err != nil {
		t.Fatalf("Parse of a %d-character part: %v", MaxPart, err)
	}
}

// TestZeroName sees the zero Name have no form and no role, so the
// checks refuse it.
func TestZeroName(t *testing.T) {
	var n Name
	if n.Form() != "" || n.Role() != "" || n.Role().Valid() {
		t.Errorf("zero Name has form %q role %q", n.Form(), n.Role())
	}
}
