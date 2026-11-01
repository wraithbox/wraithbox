package guestsched

import (
	"errors"
	"fmt"
)

// Windows guests are later (S12-platforms). Until they are built, Lock
// and Clear return an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("guestsched: cron and at on windows: %w", errors.ErrUnsupported)

// Lock closes cron and at to every user but root.
func Lock() error { return errUnsupported }

// Clear removes a user's crontab and at jobs.
func (System) Clear(string, uint32) (Cleared, error) { return Cleared{}, errUnsupported }
