package tty

import (
	"errors"
	"fmt"
	"io"
)

// Open isn't built on windows, whose hosts are later (S12-platforms). It
// returns an error that wraps errors.ErrUnsupported, so a confirmation
// is refused.
func Open() (io.ReadWriteCloser, error) { return nil, errUnsupported }

var errUnsupported = fmt.Errorf("tty: the controlling terminal on windows: %w", errors.ErrUnsupported)
