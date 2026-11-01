package volspace

import (
	"errors"
	"fmt"
)

// Windows hosts are later (S12-platforms). Until they are built, every
// call returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("volspace: volume space is not supported for this host yet (windows): %w", errors.ErrUnsupported)

func stat(string) (Space, error)       { return Space{}, errUnsupported }
func allocated(string) (uint64, error) { return 0, errUnsupported }
func allocate(string, int64) error     { return errUnsupported }
