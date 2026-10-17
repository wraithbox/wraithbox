package paths

import (
	"errors"
	"fmt"
	"log/slog"
)

// Linux hosts are later (S12-platforms). Their roots come from the XDG
// base directories (S12-01-platform-interfaces, "Paths"). Until they are
// built, every function returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("paths: host paths on linux: %w", errors.ErrUnsupported)

// Current isn't built on linux.
func Current(*slog.Logger) (Paths, error) { return Paths{}, errUnsupported }

// Real isn't built on linux.
func (Paths) Real() (Paths, error) { return Paths{}, errUnsupported }
