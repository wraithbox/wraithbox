package paths

import (
	"errors"
	"fmt"
	"log/slog"
)

// Windows hosts are later (S12-platforms). Their roots are in the
// roaming and local application data folders (S12-01-platform-interfaces,
// "Paths").
// Until they are built, every function returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("paths: host paths on windows: %w", errors.ErrUnsupported)

// Current isn't built on windows.
func Current(*slog.Logger) (Paths, error) { return Paths{}, errUnsupported }

// Real isn't built on windows.
func (Paths) Real() (Paths, error) { return Paths{}, errUnsupported }
