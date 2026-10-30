package guestprocs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// System isn't built on linux, whose guests are later (S12-platforms).
type System struct{ Log *slog.Logger }

// errUnsupported is the error of every call here.
var errUnsupported = fmt.Errorf("guestprocs: project user processes in a linux guest: %w", errors.ErrUnsupported)

// ByUID returns an error that wraps errors.ErrUnsupported.
func (*System) ByUID(uint32) ([]Proc, error) { return nil, errUnsupported }

// InRange returns an error that wraps errors.ErrUnsupported.
func (*System) InRange(uint32, uint32) ([]Proc, error) { return nil, errUnsupported }

// Signal returns an error that wraps errors.ErrUnsupported.
func (*System) Signal(Proc, Signal) error { return errUnsupported }

// Bootout returns an error that wraps errors.ErrUnsupported.
func (*System) Bootout(context.Context, uint32) error { return errUnsupported }
