// Package tty opens wb's own terminal, the controlling terminal of the
// process, for a confirmation that a person must give
// (SEC14-no-fake-approvals). It doesn't use standard input or output:
// a pipe there can't answer, and a session's stream never reaches it
// (S05-03-management-commands, "`wb vm reset-ca`").
//
// It is in internal/platform because the controlling terminal is per OS
// (S12-01-platform-interfaces, "Rules"). Linux and Windows return an
// error that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
package tty
