package packetlink

import (
	"errors"
	"fmt"
	"os"
)

// The packet transport on windows is decided with its hypervisor
// (S12-01-platform-interfaces, "Packet transport", X11-windows-host).
// Until it is built, every function returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("packetlink: packet transport on windows: %w", errors.ErrUnsupported)

// Transport is the host end of a VM's virtual NIC. On windows it isn't
// built.
type Transport struct{}

// New isn't built on windows. It closes f, as on darwin.
func New(f *os.File, _ int) (*Transport, error) {
	if f != nil {
		_ = f.Close()
	}
	return nil, errUnsupported
}

// Buffers isn't built on windows.
func (*Transport) Buffers() (snd, rcv int, err error) { return 0, 0, errUnsupported }

// ReadFrame isn't built on windows.
func (*Transport) ReadFrame([]byte) (int, error) { return 0, errUnsupported }

// WriteFrame isn't built on windows.
func (*Transport) WriteFrame([]byte) error { return errUnsupported }

// Close isn't built on windows.
func (*Transport) Close() error { return errUnsupported }
