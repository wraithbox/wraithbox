package tty

import (
	"errors"
	"fmt"
	"io"
)

// Nothing of this package is built on windows, whose hosts are later
// (S12-platforms). Each call returns an error that wraps
// errors.ErrUnsupported and says the host isn't supported yet, so a
// confirmation is refused and the relay doesn't start.

var errUnsupported = fmt.Errorf("tty: wb's terminal on windows: not supported on this host yet: %w", errors.ErrUnsupported)

// Terminal has no calls that work on windows.
type Terminal struct{}

// Open returns errUnsupported.
func Open() (io.ReadWriteCloser, error) { return nil, errUnsupported }

// NewTerminal returns errUnsupported.
func NewTerminal(int) (*Terminal, error) { return nil, errUnsupported }

// IsTerminal reports false: the relay doesn't run on windows.
func IsTerminal(int) bool { return false }

// MakeRaw returns errUnsupported.
func (*Terminal) MakeRaw() (func() error, error) { return nil, errUnsupported }

// Size returns errUnsupported.
func (*Terminal) Size() (rows, cols int, err error) { return 0, 0, errUnsupported }

// Notify returns errUnsupported.
func Notify(chan<- Signal) (stop func(), err error) { return nil, errUnsupported }

// StopSelf returns errUnsupported.
func StopSelf() error { return errUnsupported }
