// Package diskclone makes the copy-on-write copy of a disk image that a
// VM bundle gets: the guest tools disk, before every cold start
// (S06-12-guest-agent, "Guest tools disk"; S12-01-platform-interfaces,
// "Disk cloning").
//
// Cloner is the cloner of wb-hostd's VM manager (internal/hostd/vm). The
// manager deletes a leftover temporary name, calls Clone to that name,
// and renames it over the bundle's copy. Clone creates its destination
// exclusively and follows no symbolic link, at either name, so a bundle
// a possibly compromised wb-vmd wrote can't point the clone elsewhere.
// It never opens a file at the destination (SEC15-no-guest-disk-mount).
//
// On macOS the copy is an APFS clone, fclonefileat(2). Linux and
// Windows return an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
package diskclone

// Cloner makes clones. The zero value is ready to use.
type Cloner struct{}
