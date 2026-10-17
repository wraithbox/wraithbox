package tty

import (
	"errors"
	"fmt"
	"io"
)

// Open isn't built on linux, whose hosts are later (S12-platforms). It
// returns an error that wraps errors.ErrUnsupported, so a confirmation
// is refused.
func Open() (io.ReadWriteCloser, error) { return nil, errUnsupported }

var errUnsupported = fmt.Errorf("tty: the controlling terminal on linux: %w", errors.ErrUnsupported)
