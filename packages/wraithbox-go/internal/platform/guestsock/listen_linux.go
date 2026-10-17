package guestsock

import (
	"errors"
	"fmt"
)

// Linux guests are later (S12-platforms). Their listener will be AF_VSOCK
// as on darwin, where the port needs CAP_NET_BIND_SERVICE
// (S06-12-guest-agent, "The port"). Until it is built, Listen returns an
// error that wraps errors.ErrUnsupported (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
var errUnsupported = fmt.Errorf("guestsock: the wb-guestd listener on linux: %w", errors.ErrUnsupported)

func bindPort(uint32) (socket, error) { return nil, errUnsupported }
