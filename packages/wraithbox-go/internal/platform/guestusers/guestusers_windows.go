package guestusers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Dscl is the program System runs on macOS. There is none on windows.
const Dscl = ""

// System isn't built on windows, whose guests are later (S12-platforms).
type System struct{ Log *slog.Logger }

// errUnsupported is the error of every call here.
var errUnsupported = fmt.Errorf("guestusers: project users in a windows guest: %w", errors.ErrUnsupported)

// List returns an error that wraps errors.ErrUnsupported.
func (*System) List(context.Context, Kind, []string) ([]Record, error) { return nil, errUnsupported }

// Read returns an error that wraps errors.ErrUnsupported.
func (*System) Read(context.Context, Kind, string) (Record, bool, error) {
	return nil, false, errUnsupported
}

// Create returns an error that wraps errors.ErrUnsupported.
func (*System) Create(context.Context, Kind, string, string, string) error { return errUnsupported }

// OpenRoot returns an error that wraps errors.ErrUnsupported.
func OpenRoot(string) (Root, error) { return nil, errUnsupported }
