package sessionproc

import "golang.org/x/sys/unix"

// decode reads a wait status as XNU's sys/wait.h does. x/sys's
// WaitStatus reads a stop by SIGSTOP as a continue on darwin, so it
// isn't used. ok is false for a status that is none of the four.
func decode(w uint32) (Status, bool) {
	low := w & 0o177
	switch {
	case w&0xffff != w:
		return Status{}, false
	case low == 0:
		return Status{Event: Exited, Code: int(w>>8) & 0xff}, true
	case low == 0o177 && unix.Signal(w>>8&0xff) == unix.SIGCONT:
		return Status{Event: Continued}, true
	case low == 0o177:
		return Status{Event: Stopped}, true
	}
	return Status{Event: Signaled, Signal: int(low)}, true
}
