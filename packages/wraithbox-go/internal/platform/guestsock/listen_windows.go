package guestsock

import (
	"errors"
	"fmt"
)

// Windows guests are later (S12-platforms), and how they keep a project
// user from posing as wb-guestd is a spike's question
// (S12-01-platform-interfaces, "Host-guest socket: keeping `wb-guestd`'s
// endpoint"). Until it is built, Listen returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
var errUnsupported = fmt.Errorf("guestsock: the wb-guestd listener on windows: %w", errors.ErrUnsupported)

func bindPort(uint32) (socket, error) { return nil, errUnsupported }
