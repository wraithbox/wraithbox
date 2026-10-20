package vmhandoff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
)

// Linux hosts reach the guest's vsock without wb-vmd's hand-off
// (S12-01-platform-interfaces, "Host-guest socket"). Every function
// returns an error that wraps errors.ErrUnsupported
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("vmhandoff: the wb-vmd hand-off on linux: %w", errors.ErrUnsupported)

// waiter is empty: no request is ever opened on linux.
type waiter struct{}

func newWaiter() waiter { return waiter{} }

// Receiver is wb-hostd's end of a hand-off channel. On linux it isn't
// built.
type Receiver struct{}

// Pending is an open request. On linux it isn't built.
type Pending struct{}

// NewReceiver isn't built on linux. It closes f, as on darwin.
func NewReceiver(f *os.File, _ *slog.Logger) (*Receiver, error) {
	if f != nil {
		_ = f.Close()
	}
	return nil, errUnsupported
}

// Expect isn't built on linux.
func (*Receiver) Expect(uint64, uint64, uint32) (*Pending, error) { return nil, errUnsupported }

// Run isn't built on linux.
func (*Receiver) Run() error { return errUnsupported }

// Receive isn't built on linux.
func (*Receiver) Receive() error { return errUnsupported }

// Close isn't built on linux.
func (*Receiver) Close() error { return errUnsupported }

// Wait isn't built on linux.
func (*Pending) Wait(context.Context) (net.Conn, error) { return nil, errUnsupported }

// Cancel does nothing on linux.
func (*Pending) Cancel() {}
