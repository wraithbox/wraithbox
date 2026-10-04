// Package sbxapply picks a mechanism by name and applies a profile file.
package sbxapply

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"x23/sbxcgo"
	"x23/sbxpure"
)

// Params collects -D key=value flags.
type Params []string

func (p *Params) String() string { return strings.Join(*p, ",") }

// Set parses key=value.
func (p *Params) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("want key=value, got %q", s)
	}
	*p = append(*p, k, v)
	return nil
}

// Apply applies profile with mech: none, cgo or pure. It returns how long
// the call took.
func Apply(mech, profileFile string, params Params) (time.Duration, error) {
	if mech == "none" || mech == "" {
		return 0, nil
	}
	var b []byte
	var err error
	if profileFile == "fd:3" { // profile handed over on a pipe, no path needed
		b, err = io.ReadAll(os.NewFile(3, "profile"))
	} else {
		b, err = os.ReadFile(profileFile)
	}
	if err != nil {
		return 0, err
	}
	start := time.Now()
	switch mech {
	case "cgo":
		err = sbxcgo.Apply(string(b), params)
	case "pure":
		err = sbxpure.Apply(string(b), params)
	default:
		err = fmt.Errorf("unknown mechanism %q", mech)
	}
	return time.Since(start), err
}
