// Package storeitem holds the names of the secret store's items, the
// roles and access groups they belong to, the programs with store
// access, and the declared access of each program
// (S15-02-secret-store, "Keychain"; S15-03-access-rules, "Declared
// access").
//
// It is pure Go with no cgo, so wb, wb-hostd and the secret store
// backends can all use it. Only the programs with store access import
// internal/platform/secretstore, which does the store calls.
package storeitem

import (
	"errors"
	"fmt"
	"strings"
)

// Prefix starts every item name (S15-03-access-rules, "Declared
// access").
const Prefix = "wraithbox/"

// Role is the first part of an item name after Prefix. Each role has one
// access group (S15-02-secret-store, "Keychain").
type Role string

// The roles of S15-02-secret-store, "Keychain".
const (
	RoleBinding Role = "binding"
	RoleCA      Role = "ca"
	RoleWbadmin Role = "wbadmin"
)

// Roles lists every role.
func Roles() []Role { return []Role{RoleBinding, RoleCA, RoleWbadmin} }

// Valid reports whether r is one of the roles.
func (r Role) Valid() bool {
	switch r {
	case RoleBinding, RoleCA, RoleWbadmin:
		return true
	}
	return false
}

// Group returns the access group of role r for the team that signed the
// build: <TEAMID>.nl.wraithbox.<role> (S15-02-secret-store, "Keychain").
func Group(team string, r Role) string {
	return team + ".nl.wraithbox." + string(r)
}

// Form is one of the item name forms of S15-03-access-rules, "Declared
// access". Its value is the form as the spec writes it, after Prefix.
type Form string

// The item name forms. Each <…> part is 1 to 64 characters of a to z, 0
// to 9 and -.
const (
	FormBinding      Form = "binding/<name>"
	FormCA           Form = "ca/<vm>/<ca>"
	FormWbadminBuild Form = "wbadmin/build/<build>"
	FormWbadminImage Form = "wbadmin/image/<image>"
	FormWbadminVM    Form = "wbadmin/vm/<vm>/<disk>"
)

// Forms lists every item name form.
func Forms() []Form {
	return []Form{FormBinding, FormCA, FormWbadminBuild, FormWbadminImage, FormWbadminVM}
}

// Role returns the role of the items of form f, the first part of the
// form, or "" for a value that isn't a form.
func (f Form) Role() Role {
	switch f {
	case FormBinding:
		return RoleBinding
	case FormCA:
		return RoleCA
	case FormWbadminBuild, FormWbadminImage, FormWbadminVM:
		return RoleWbadmin
	}
	return ""
}

// MaxPart is the longest <…> part of an item name.
const MaxPart = 64

// Name is a parsed item name. The zero Name is no item: its form and
// role are "", so every check that compares them refuses it.
type Name struct {
	full string
	form Form
}

// String returns the full item name, with Prefix.
func (n Name) String() string { return n.full }

// Form returns the form of the name.
func (n Name) Form() Form { return n.form }

// Role returns the role of the name, the first part after Prefix.
func (n Name) Role() Role { return n.form.Role() }

// ErrName is the error of every name Parse refuses.
var ErrName = errors.New("storeitem: not an item name")

// Parse returns the item name s, which must be Prefix followed by one of
// the forms of S15-03-access-rules, "Declared access", with every <…>
// part of 1 to MaxPart characters of a to z, 0 to 9 and -. Anything else
// is an error that wraps ErrName. The CA removal list holds names that
// a compromised wb-hostd may have chosen (T16-hostd-landing), so this is
// the only way to make a Name.
func Parse(s string) (Name, error) {
	rest, ok := strings.CutPrefix(s, Prefix)
	if !ok {
		return Name{}, fmt.Errorf("%w: %q doesn't start with %q", ErrName, s, Prefix)
	}
	parts := strings.Split(rest, "/")
	form, err := formOf(parts)
	if err != nil {
		return Name{}, fmt.Errorf("%w: %q: %w", ErrName, s, err)
	}
	return Name{full: s, form: form}, nil
}

// formOf returns the form of the parts after Prefix: the form whose
// literal parts are equal to them, where every <…> part is valid.
func formOf(parts []string) (Form, error) {
	for _, f := range Forms() {
		pattern := strings.Split(string(f), "/")
		if !sameLiterals(pattern, parts) {
			continue
		}
		for i, p := range pattern {
			if isVariable(p) && !ValidPart(parts[i]) {
				return "", fmt.Errorf("part %s is not 1 to %d characters of a-z, 0-9 and -", p, MaxPart)
			}
		}
		return f, nil
	}
	return "", errors.New("no item form has these parts")
}

// sameLiterals reports whether parts has as many parts as pattern, and
// the same literal parts, such as "wbadmin" and "vm".
func sameLiterals(pattern, parts []string) bool {
	if len(pattern) != len(parts) {
		return false
	}
	for i, p := range pattern {
		if !isVariable(p) && parts[i] != p {
			return false
		}
	}
	return true
}

// isVariable reports whether a part of a form is a <…> part.
func isVariable(p string) bool { return strings.HasPrefix(p, "<") }

// ValidPart reports whether s is a valid <…> part of an item name: 1 to
// MaxPart characters of a to z, 0 to 9 and -.
func ValidPart(s string) bool {
	if s == "" || len(s) > MaxPart {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
