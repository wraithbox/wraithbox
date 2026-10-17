// Package diskimage is the platform half of wb-diskfmt: the file system
// calls and the disk tool that internal/diskfmt runs its steps with
// (S06-13-data-disk-format, "Formatting a data disk"; S12-01-platform-interfaces).
//
// On macOS, Files makes the system calls directly (open with
// O_CREAT|O_EXCL|O_NOFOLLOW, ftruncate, lstat, fstat, unlink, and
// renamex_np with RENAME_EXCL), and Tool runs /usr/sbin/diskutil and
// reads an attached device's backing image path from the I/O Registry
// in-process, without another program. Linux and Windows return an
// error that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
//
// The parsers of diskutil's output are in parse.go, with no OS code, so
// their table tests run on every OS. That output comes from a blank
// image wb-diskfmt made, never from a guest, so it gets table tests and
// no fuzz target.
package diskimage
