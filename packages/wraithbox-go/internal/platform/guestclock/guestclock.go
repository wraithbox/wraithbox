// Package guestclock sets the guest's wall clock, for wb-guestd's
// SetTime: a step to a time, or a slew by an offset
// (S06-06-devices-and-warm-start, "Step or slew"). Both need root.
//
// The calls are per OS (S12-01-platform-interfaces, "Rules"). On darwin
// they are settimeofday and adjtime through golang.org/x/sys/unix. Linux
// and Windows guests are later, and both calls there return an error
// that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
package guestclock
