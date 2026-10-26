// Package guestusers holds the in-guest conformance checks of the
// project users, which wb-guestd's EnsureUsers makes
// (C09-guest-and-sessions, "Project users"; S06-03-image-updates,
// "Creation"; SEC08-proj-isolation; SEC12-least-privilege).
//
// The checks are tests behind the build tag `conformance`, so `go test
// ./...` and CI never run them. `mise run conformance:build` compiles
// them for darwin/arm64 into .build/conformance/guestusers.test, and
// they are run by hand as root in a test guest on the maintainer's Mac,
// after wb-hostd had wb-guestd make at least two project users
// (S11-verification-and-spikes, "Conformance suite"). Each check skips,
// saying why, when it doesn't run in a guest, doesn't run as root, or
// finds too few project users. No check writes a record or a home: the
// one that calls EnsureUsers' code does so through a directory that
// refuses every write.
package guestusers
