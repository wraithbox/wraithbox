package guestclock

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// Step sets the clock to t at once, with settimeofday. The clock can go
// backward.
func Step(t time.Time) error {
	tv := unix.NsecToTimeval(t.UnixNano())
	if err := unix.Settimeofday(&tv); err != nil {
		return fmt.Errorf("guestclock: settimeofday: %w", err)
	}
	return nil
}

// Slew moves the clock by d over time, with adjtime: the kernel runs the
// clock a little fast or slow until it has gained or lost d, so the
// clock never goes backward. A slew replaces one still in progress.
func Slew(d time.Duration) error {
	tv := unix.NsecToTimeval(d.Nanoseconds())
	if err := unix.Adjtime(&tv, nil); err != nil {
		return fmt.Errorf("guestclock: adjtime: %w", err)
	}
	return nil
}
