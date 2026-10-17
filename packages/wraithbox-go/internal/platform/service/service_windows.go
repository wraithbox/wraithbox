package service

import (
	"errors"
	"fmt"
)

// New returns an error that wraps errors.ErrUnsupported. On Windows the
// service is a per-user logon task, which comes with Windows hosts
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func New(Config) (Manager, error) {
	return nil, fmt.Errorf("service: the logon task comes with Windows hosts, which are later: %w", errors.ErrUnsupported)
}
