package guestsched

import (
	"errors"
	"fmt"
)

// Windows guests are later (S12-platforms). Until they are built,
// CheckAllow, Scan and Find return an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("guestsched: cron and at on windows: %w", errors.ErrUnsupported)

// CheckAllow checks that cron and at are closed to every user but root.
func CheckAllow() error { return errUnsupported }

// Find counts a user's crontab and at jobs.
func (System) Find(string, uint32) (Found, error) { return Found{}, errUnsupported }

// Scan counts every project user's crontabs and at jobs.
func Scan() (Found, error) { return Found{}, errUnsupported }
