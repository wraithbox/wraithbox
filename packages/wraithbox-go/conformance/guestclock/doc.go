// Package guestclock holds the in-guest conformance checks of setting
// the guest's clock, which wb-guestd's SetTime does
// (C09-guest-and-sessions, "From the guest";
// S06-06-devices-and-warm-start, "Step or slew").
//
// The checks are tests behind the build tag `conformance`, so `go test
// ./...` and CI never run them. `mise run conformance:build` compiles
// them for darwin/arm64 into .build/conformance/guestclock.test, and they
// are run by hand as root in a test guest on the maintainer's Mac
// (S11-verification-and-spikes, "Conformance suite"). Each check skips,
// saying why, when it doesn't run in a guest or not as root. They change
// the guest's clock, and set it back.
package guestclock
