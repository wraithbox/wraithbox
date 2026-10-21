package guestclock

import (
	"errors"
	"fmt"
	"time"
)

// Linux guests are later (S12-platforms). Until they are built, Step and
// Slew return an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("guestclock: setting the clock on linux: %w", errors.ErrUnsupported)

// Step sets the clock to t at once.
func Step(time.Time) error { return errUnsupported }

// Slew moves the clock by d over time.
func Slew(time.Duration) error { return errUnsupported }
