package sessionproc

// decode reads a wait status as glibc's bits/waitstatus.h does. ok is
// false for a status that is none of the four.
func decode(w uint32) (Status, bool) {
	switch {
	case w&0xffff != w:
		return Status{}, false
	case w == 0xffff:
		return Status{Event: Continued}, true
	case w&0x7f == 0:
		return Status{Event: Exited, Code: int(w>>8) & 0xff}, true
	case w&0xff == 0x7f:
		return Status{Event: Stopped}, true
	}
	return Status{Event: Signaled, Signal: int(w & 0x7f)}, true
}
