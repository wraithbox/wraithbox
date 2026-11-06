// Package fileowner reads the user ID that owns a file. wb-guestd
// refuses a trust directory that root doesn't own (S09-08-guest-trust,
// "Files").
//
// macOS and Linux read it from the stat(2) result. Windows returns an
// error that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
package fileowner

import "io/fs"

// UID returns the user ID that owns the file info describes. info must
// come from os.Stat, os.Lstat, an os.Root's Stat or Lstat, or an
// os.File's Stat.
func UID(info fs.FileInfo) (uint32, error) { return uid(info) }
