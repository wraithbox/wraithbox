// Package shells holds wb-hostd's debug shells of V1-M2
// (S06-09-session-lifecycle): the checks of the frames wb-guestd sends
// on a shell's stream, the registry of open shells, and the relay
// between wb's ShellService stream and wb-guestd's GuestService.Shell
// stream.
//
// Every frame from the guest is adversarial input
// (SEC11-root-gains-nothing). The relay checks each one against the
// bounds of wraithbox.terminal.v1 and rebuilds the frame wb gets, so no
// field the guest set but this build doesn't check reaches wb. A frame
// out of bounds ends the shell with a failure, and the log names the
// rule.
package shells
