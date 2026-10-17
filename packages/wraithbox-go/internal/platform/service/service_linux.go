package service

import (
	"errors"
	"fmt"
)

// New returns an error that wraps errors.ErrUnsupported. On Linux the
// service is a systemd user unit, which comes with Linux hosts
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func New(Config) (Manager, error) {
	return nil, fmt.Errorf("service: the systemd user unit comes with Linux hosts, which are later: %w", errors.ErrUnsupported)
}
