// Package version holds the Wraith Box build version of every Go program
// in this module, and the exact-match check between two peers' versions
// (S04-08-records-and-bounds, "Version skew"; S10-02-packaging-and-signing,
// "One build version").
package version

import (
	"fmt"
	"strconv"
)

// importPath is this package's import path, for the linker's -X flag.
const importPath = "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"

// StampVar is the name the build tasks give the linker to stamp the
// version: `go build -ldflags "-X <StampVar>=<version>"`.
// internal/tools/buildversion does that for `mise run build` and CI.
const StampVar = importPath + ".stamp"

// stamp is set at link time by the build tasks (StampVar). A bare
// `go build`, `go run` or `go test` leaves it empty: an unstamped build,
// which has no version.
var stamp string

// Build returns this program's build version. It returns "" when the
// build is unstamped, or when its stamp isn't in the version form (Valid).
// "" is no version: Check refuses every peer for it.
func Build() string {
	if !Valid(stamp) {
		return ""
	}
	return stamp
}

// unstampedLabel is what a version line shows for an unstamped build.
const unstampedLabel = "unstamped"

// String formats the version line every binary prints for --version.
func String(binary string) string {
	v := Build()
	if v == "" {
		v = unstampedLabel
	}
	return binary + " (Wraith Box) " + v
}

// MaxLen is the most bytes a version has (S04-08-records-and-bounds,
// "The guest's version is a label").
const MaxLen = 64

// Valid reports whether s is in the version form of
// S04-08-records-and-bounds, "The guest's version is a label": printable
// ASCII, 1 to MaxLen bytes, MAJOR.MINOR.PATCH with an optional `-`
// prerelease part and an optional `+` build part, each of letters,
// digits, dots and hyphens. It reads s byte by byte, so it never looks
// past MaxLen bytes.
func Valid(s string) bool {
	if s == "" || len(s) > MaxLen {
		return false
	}
	rest, ok := numbers(s)
	if ok {
		rest, ok = optionalPart(rest, '-')
	}
	if ok {
		rest, ok = optionalPart(rest, '+')
	}
	return ok && rest == ""
}

// numbers reads MAJOR.MINOR.PATCH, each one or more ASCII digits, from
// the start of s and returns what follows.
func numbers(s string) (string, bool) {
	for i := range 3 {
		if i > 0 {
			if s == "" || s[0] != '.' {
				return "", false
			}
			s = s[1:]
		}
		n := 0
		for n < len(s) && isDigit(s[n]) {
			n++
		}
		if n == 0 {
			return "", false
		}
		s = s[n:]
	}
	return s, true
}

// optionalPart reads a part that starts with sep from the start of s:
// sep, then letters, digits, dots and hyphens up to the next `+` or any
// other byte, and returns what follows. When s doesn't start with sep
// there is no part, and it returns s. ok is false for an empty part.
func optionalPart(s string, sep byte) (rest string, ok bool) {
	if s == "" || s[0] != sep {
		return s, true
	}
	n := 1
	for n < len(s) && isPartByte(s[n]) {
		n++
	}
	return s[n:], n > 1
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// isPartByte reports whether b may be in a prerelease or build part. A
// `+` is not: it starts the build part and ends the prerelease part.
func isPartByte(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '.' || b == '-'
}

// The rules a Refusal names, for the caller's log record.
const (
	// RuleUnstamped: this side has no version (empty, or its stamp is
	// outside the version form), or the peer's version is empty.
	RuleUnstamped = "peer-version-unstamped"
	// RuleForm: the peer sent a version that isn't empty and isn't in
	// the version form (S04-08-records-and-bounds, "The guest's version
	// is a label").
	RuleForm = "guest-version-form"
	// RuleMismatch: both sides have a version, and they differ as bytes.
	RuleMismatch = "peer-version-mismatch"
)

// Refusal is the error Check returns. It keeps the raw versions to
// itself: a peer's version is adversarial input, so OwnLabel, PeerLabel
// and Error show each one only through Label.
type Refusal struct {
	Rule string
	own  string
	peer string
}

// OwnLabel is this side's version, as Label shows it.
func (r *Refusal) OwnLabel() string { return Label(r.own) }

// PeerLabel is the peer's version, as Label shows it.
func (r *Refusal) PeerLabel() string { return Label(r.peer) }

func (r *Refusal) Error() string {
	return fmt.Sprintf("version refused (%s): own %s, peer %s", r.Rule, r.OwnLabel(), r.PeerLabel())
}

// Check compares a peer's build version with this side's own, and
// returns nil only when both are versions in the form and equal as
// bytes. Host processes and wb-guestd have no compatibility rules, so
// any other version is refused (S04-08-records-and-bounds, "Version
// skew"). An unstamped side has no version: two unstamped peers refuse
// each other (S10-02-packaging-and-signing, "One build version"). The
// caller passes own explicitly, normally Build(), and logs the refusal
// with its rule and both labels.
func Check(own, peer string) error {
	switch {
	case !Valid(own), peer == "":
		return &Refusal{Rule: RuleUnstamped, own: own, peer: peer}
	case !Valid(peer):
		return &Refusal{Rule: RuleForm, own: own, peer: peer}
	case own != peer:
		return &Refusal{Rule: RuleMismatch, own: own, peer: peer}
	}
	return nil
}

// Label returns a version, or any string a peer reported as one, in a
// form safe to show in a log record or a message: cut to MaxLen bytes,
// then quoted with every byte outside printable ASCII escaped
// (S04-08-records-and-bounds, "The guest's version is a label"). The
// empty string shows as `unstamped`.
func Label(s string) string {
	if s == "" {
		return unstampedLabel
	}
	if len(s) > MaxLen {
		s = s[:MaxLen]
	}
	return strconv.QuoteToASCII(s)
}
