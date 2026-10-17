package localipc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Windows hosts are later (S12-platforms). Their endpoint will be a
// named pipe with an ACL for the user's SID (S12-01-platform-interfaces,
// "Local IPC"). Until it is built, every function returns an error that
// wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("localipc: local IPC on windows: %w", errors.ErrUnsupported)

// EnsureRunDir isn't built on windows.
func EnsureRunDir(*slog.Logger, string) error { return errUnsupported }

// CheckRunDir isn't built on windows.
func CheckRunDir(*slog.Logger, string) error { return errUnsupported }

// Listen isn't built on windows.
func Listen(context.Context, *slog.Logger, string, string) (*Listener, error) {
	return nil, errUnsupported
}

// Dial isn't built on windows.
func Dial(context.Context, *slog.Logger, string, string) (*Conn, error) {
	return nil, errUnsupported
}
