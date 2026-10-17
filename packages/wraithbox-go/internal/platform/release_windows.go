package platform

import (
	"errors"
	"fmt"
)

// ReadRelease returns an error that wraps errors.ErrUnsupported: Windows
// hosts are later in the platform matrix (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly").
func ReadRelease() (Release, error) {
	return Release{}, fmt.Errorf("read the host release: Windows hosts are later: %w", errors.ErrUnsupported)
}
