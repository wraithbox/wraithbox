package fdchan

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Windows hosts are later (S12-platforms), and their mechanism isn't
// chosen yet. Until it is built, every function returns an error that
// wraps errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("fdchan: descriptor channels on windows: %w", errors.ErrUnsupported)

// Channel is one end of a descriptor channel. On windows it isn't built.
type Channel struct{}

// Socketpair isn't built on windows.
func Socketpair(int) (a, b *os.File, err error) { return nil, nil, errUnsupported }

// New isn't built on windows. It closes f, as on darwin.
func New(f *os.File, _ int) (*Channel, error) {
	if f != nil {
		_ = f.Close()
	}
	return nil, errUnsupported
}

// SocketType isn't built on windows.
func SocketType(syscall.Conn) (family, sockType int, err error) { return 0, 0, errUnsupported }

// Send isn't built on windows.
func (*Channel) Send([]byte, syscall.Conn) error { return errUnsupported }

// Recv isn't built on windows.
func (*Channel) Recv() (*Message, error) { return nil, errUnsupported }

// SetReadDeadline isn't built on windows.
func (*Channel) SetReadDeadline(time.Time) error { return errUnsupported }

// SetWriteDeadline isn't built on windows.
func (*Channel) SetWriteDeadline(time.Time) error { return errUnsupported }

// Close isn't built on windows.
func (*Channel) Close() error { return errUnsupported }
