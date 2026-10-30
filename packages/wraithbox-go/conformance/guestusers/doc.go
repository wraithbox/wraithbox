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
// finds too few project users. The check that calls EnsureUsers' code
// does so through a directory that refuses every write. Two checks
// write, and each runs only when its variable names the UID of a
// project user it may end: TestKillUser, with WB_CONFORMANCE_KILL_UID,
// kills the user's processes, and TestRemoveUser, with
// WB_CONFORMANCE_REMOVE_UID, removes the user's home and retires its
// record (C09-guest-and-sessions, "Locking, killing and removing").
package guestusers
