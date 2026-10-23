package diskclone

import (
	"errors"
	"fmt"
)

// Windows hosts are later (S12-platforms). Their clone will be a VHDX
// differencing disk (S12-01-platform-interfaces, "Disk cloning"). Until
// it is built, Clone returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("diskclone: disk cloning is not supported for this host yet (windows): %w", errors.ErrUnsupported)

// Clone isn't built on windows.
func (Cloner) Clone(_, _ string) error { return errUnsupported }
