package fdchan

// FirstOpenFrom isn't built on windows.
func FirstOpenFrom(int) (int, error) { return 0, errUnsupported }
