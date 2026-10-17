package selfsandbox

import (
	"errors"
	"fmt"
)

// Confine isn't built on linux, whose hosts are later (S12-platforms).
// The mechanism there is Landlock and seccomp (S12-01-platform-interfaces,
// "Self-sandbox"). It returns an error that wraps errors.ErrUnsupported
// and leaves the process unconfined, so a daemon that calls it exits
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func Confine(Profile, map[string]string) error { return errUnsupported }

var errUnsupported = fmt.Errorf("selfsandbox: confinement on linux: %w", errors.ErrUnsupported)
