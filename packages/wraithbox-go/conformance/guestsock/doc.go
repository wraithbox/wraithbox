// Package guestsock holds the in-guest conformance checks of wb-guestd's
// host-guest socket (C09-guest-and-sessions, "From the guest";
// X27-vsock-confinement, conditions 1, 3 and 4; #115 item 1).
//
// The checks are tests behind the build tag `conformance`, so `go test
// ./...` and CI never run them. `mise run conformance:build` compiles
// them for darwin/arm64 into .build/conformance/guestsock.test, and they
// are run by hand in a test guest on the maintainer's Mac
// (S11-verification-and-spikes, "Conformance suite"). Each check skips,
// saying why, when it doesn't run in a guest or not as the user it
// needs.
package guestsock
