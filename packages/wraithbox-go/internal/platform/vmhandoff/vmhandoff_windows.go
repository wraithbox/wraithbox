package vmhandoff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
)

// Windows hosts reach the guest over Hyper-V sockets
// (S12-01-platform-interfaces, "Host-guest socket"). Every function
// returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("vmhandoff: the wb-vmd hand-off on windows: %w", errors.ErrUnsupported)

// waiter is empty: no request is ever opened on windows.
type waiter struct{}

func newWaiter() waiter { return waiter{} }

// Receiver is wb-hostd's end of a hand-off channel. On windows it isn't
// built.
type Receiver struct{}

// Pending is an open request. On windows it isn't built.
type Pending struct{}

// NewReceiver isn't built on windows. It closes f, as on darwin.
func NewReceiver(f *os.File, _ *slog.Logger) (*Receiver, error) {
	if f != nil {
		_ = f.Close()
	}
	return nil, errUnsupported
}

// Expect isn't built on windows.
func (*Receiver) Expect(uint64, uint64, uint32) (*Pending, error) { return nil, errUnsupported }

// Run isn't built on windows.
func (*Receiver) Run() error { return errUnsupported }

// Receive isn't built on windows.
func (*Receiver) Receive() error { return errUnsupported }

// Close isn't built on windows.
func (*Receiver) Close() error { return errUnsupported }

// Wait isn't built on windows.
func (*Pending) Wait(context.Context) (net.Conn, error) { return nil, errUnsupported }

// Cancel does nothing on windows.
func (*Pending) Cancel() {}
