package packetlink

import (
	"errors"
	"fmt"
	"os"
)

// The packet transport on linux is decided with its hypervisor
// (S12-01-platform-interfaces, "Packet transport", X10-linux-hypervisor).
// Until it is built, every function returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("packetlink: packet transport on linux: %w", errors.ErrUnsupported)

// Transport is the host end of a VM's virtual NIC. On linux it isn't
// built.
type Transport struct{}

// New isn't built on linux. It closes f, as on darwin.
func New(f *os.File, _ int) (*Transport, error) {
	if f != nil {
		_ = f.Close()
	}
	return nil, errUnsupported
}

// Buffers isn't built on linux.
func (*Transport) Buffers() (snd, rcv int, err error) { return 0, 0, errUnsupported }

// ReadFrame isn't built on linux.
func (*Transport) ReadFrame([]byte) (int, error) { return 0, errUnsupported }

// WriteFrame isn't built on linux.
func (*Transport) WriteFrame([]byte) error { return errUnsupported }

// Close isn't built on linux.
func (*Transport) Close() error { return errUnsupported }
