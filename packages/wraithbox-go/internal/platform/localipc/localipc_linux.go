package localipc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
)

// Linux hosts are later (S12-platforms). Their endpoint will be a Unix
// socket checked with SO_PEERCRED (S12-01-platform-interfaces, "Local
// IPC"). Until it is built, every function returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("localipc: local IPC on linux: %w", errors.ErrUnsupported)

// peerCredential isn't built on linux, so every peer is refused.
var peerCredential = func(*net.UnixConn) (Peer, error) { return Peer{}, errUnsupported }

// EnsureRunDir isn't built on linux.
func EnsureRunDir(*slog.Logger, string) error { return errUnsupported }

// CheckRunDir isn't built on linux.
func CheckRunDir(*slog.Logger, string) error { return errUnsupported }

// Listen isn't built on linux.
func Listen(context.Context, *slog.Logger, string, string) (*Listener, error) {
	return nil, errUnsupported
}

// Dial isn't built on linux.
func Dial(context.Context, *slog.Logger, string, string) (*Conn, error) {
	return nil, errUnsupported
}
