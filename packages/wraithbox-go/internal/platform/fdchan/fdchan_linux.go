package fdchan

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Linux hosts are later (S12-platforms). Their channel will be
// SOCK_SEQPACKET. Until it is built, every function returns an error that
// wraps errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("fdchan: descriptor channels on linux: %w", errors.ErrUnsupported)

// Channel is one end of a descriptor channel. On linux it isn't built.
type Channel struct{}

// Socketpair isn't built on linux.
func Socketpair(int) (a, b *os.File, err error) { return nil, nil, errUnsupported }

// New isn't built on linux. It closes f, as on darwin.
func New(f *os.File, _ int) (*Channel, error) {
	if f != nil {
		_ = f.Close()
	}
	return nil, errUnsupported
}

// SocketType isn't built on linux.
func SocketType(syscall.Conn) (family, sockType int, err error) { return 0, 0, errUnsupported }

// Send isn't built on linux.
func (*Channel) Send([]byte, syscall.Conn) error { return errUnsupported }

// Recv isn't built on linux.
func (*Channel) Recv() (*Message, error) { return nil, errUnsupported }

// SetReadDeadline isn't built on linux.
func (*Channel) SetReadDeadline(time.Time) error { return errUnsupported }

// SetWriteDeadline isn't built on linux.
func (*Channel) SetWriteDeadline(time.Time) error { return errUnsupported }

// Close isn't built on linux.
func (*Channel) Close() error { return errUnsupported }
