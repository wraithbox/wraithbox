package diskclone

import (
	"errors"
	"fmt"
)

// Linux hosts are later (S12-platforms). Their clone will be a qcow2
// backing file or a reflink (S12-01-platform-interfaces, "Disk
// cloning"). Until it is
// built, Clone returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("diskclone: disk cloning is not supported for this host yet (linux): %w", errors.ErrUnsupported)

// Clone isn't built on linux.
func (Cloner) Clone(_, _ string) error { return errUnsupported }
