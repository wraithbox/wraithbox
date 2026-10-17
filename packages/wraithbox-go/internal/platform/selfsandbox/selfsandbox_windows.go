package selfsandbox

import (
	"errors"
	"fmt"
)

// Confine isn't built on windows, whose hosts are later (S12-platforms).
// The mechanism there is a restricted token, job objects and AppContainer
// where it fits (S12-01-platform-interfaces, "Self-sandbox"). It returns
// an error that wraps errors.ErrUnsupported and leaves the process
// unconfined, so a daemon that calls it exits (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func Confine(Profile, map[string]string) error { return errUnsupported }

var errUnsupported = fmt.Errorf("selfsandbox: confinement on windows: %w", errors.ErrUnsupported)
