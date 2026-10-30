package sessionproc

import "context"

// UserTempDir returns "": linux has no per-user temporary directory of
// the system's own, so the session gets no TMPDIR there.
func UserTempDir(context.Context) (string, error) { return "", nil }
