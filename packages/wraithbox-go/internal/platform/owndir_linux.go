package platform

import (
	"errors"
	"fmt"
)

// CheckOwnDir checks that path is a directory owned by the user. Linux
// hosts come later (S12-platforms), so it returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
func CheckOwnDir(string) error { return errOwnDirUnsupported }

var errOwnDirUnsupported = fmt.Errorf("directory owner checks on linux: not supported on this host yet: %w", errors.ErrUnsupported)
