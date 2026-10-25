package guestmount

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Diskutil is the program System runs on macOS. There is none on linux.
const Diskutil = ""

// System isn't built on linux, whose guests are later (S12-platforms).
type System struct{ Log *slog.Logger }

// errUnsupported is the error of every call here.
var errUnsupported = fmt.Errorf("guestmount: mounting the data volume in a linux guest: %w", errors.ErrUnsupported)

// Info returns an error that wraps errors.ErrUnsupported.
func (*System) Info(context.Context, string) (Volume, bool, error) {
	return Volume{}, false, errUnsupported
}

// Unmount returns an error that wraps errors.ErrUnsupported.
func (*System) Unmount(context.Context, string) error { return errUnsupported }

// Mount returns an error that wraps errors.ErrUnsupported.
func (*System) Mount(context.Context, string, string) error { return errUnsupported }

// Statfs returns an error that wraps errors.ErrUnsupported.
func (*System) Statfs(string) (Mount, error) { return Mount{}, errUnsupported }

// Lstat returns an error that wraps errors.ErrUnsupported.
func (*System) Lstat(string) (Dir, error) { return Dir{}, errUnsupported }

// Mkdir returns an error that wraps errors.ErrUnsupported.
func (*System) Mkdir(string) error { return errUnsupported }

// Claim returns an error that wraps errors.ErrUnsupported.
func (*System) Claim(string) error { return errUnsupported }
