// Package datamount holds the in-guest conformance checks of the data
// volume's mount, which wb-guestd's MountData makes and checks
// (C09-guest-and-sessions, "Data volume"; S06-05-disks, "The mount";
// SEC11-root-gains-nothing).
//
// The checks are tests behind the build tag `conformance`, so `go test
// ./...` and CI never run them. `mise run conformance:build` compiles
// them for darwin/arm64 into .build/conformance/datamount.test, and they
// are run by hand in a test guest on the maintainer's Mac, after
// wb-hostd had wb-guestd mount the data volume
// (S11-verification-and-spikes, "Conformance suite"). They run as a
// standard user and as root. Each check skips, saying why, when it
// doesn't run in a guest, finds no data volume mounted, or doesn't run
// as the user it needs. The checks as root remount the volume without
// nosuid, and set it back.
package datamount
