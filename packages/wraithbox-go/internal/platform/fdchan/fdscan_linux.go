package fdchan

// FirstOpenFrom isn't built on linux.
func FirstOpenFrom(int) (int, error) { return 0, errUnsupported }

// CloseOnExecFrom isn't built on linux.
func CloseOnExecFrom(int) (int, error) { return 0, errUnsupported }
