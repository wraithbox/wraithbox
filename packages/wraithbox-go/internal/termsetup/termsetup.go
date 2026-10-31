// Package termsetup checks a wraithbox.terminal.v1 Setup against the
// contract's bounds (proto/wraithbox/terminal/v1/terminal.proto). wb-hostd
// checks it on the first message from wb, and wb-guestd again on the
// start from wb-hostd, so neither trusts the other's check
// (SEC11-root-gains-nothing).
package termsetup

import (
	"math"
	"slices"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
)

// EnvNames are the host terminal variables a shell gets
// (S16-03-around-the-stream, "Environment"), as wb sends them
// (internal/relay).
var EnvNames = []string{
	"TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM",
	"LANG", "LC_ALL", "LC_CTYPE",
}

// MaxEnvValue is the longest value of one variable.
const MaxEnvValue = 256

// WindowSize returns the rows and columns of size, and false when one
// is outside 1..65535.
func WindowSize(size *terminalv1.WindowSize) (rows, cols uint16, ok bool) {
	r, c := size.GetRows(), size.GetCols()
	if r < 1 || r > math.MaxUint16 || c < 1 || c > math.MaxUint16 {
		return 0, 0, false
	}
	return uint16(r), uint16(c), true
}

// ValidSize reports whether setup is present, and has a window size in
// range when it is interactive.
func ValidSize(setup *terminalv1.Setup) bool {
	if setup == nil {
		return false
	}
	_, _, ok := WindowSize(setup.GetSize())
	return ok || !setup.GetInteractive()
}

// ValidEnv reports whether env has only names of EnvNames, each once,
// with values of at most MaxEnvValue bytes of printable ASCII.
func ValidEnv(env []*terminalv1.EnvVar) bool {
	seen := map[string]bool{}
	for _, v := range env {
		name := v.GetName()
		if !slices.Contains(EnvNames, name) || seen[name] || !printable(v.GetValue()) {
			return false
		}
		seen[name] = true
	}
	return true
}

func printable(v string) bool {
	if len(v) > MaxEnvValue {
		return false
	}
	for _, c := range []byte(v) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
