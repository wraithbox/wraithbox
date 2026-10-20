package platform

import (
	"errors"
	"fmt"
)

// CanonicalDir returns the canonical form of a directory: links
// resolved, and letter case as on disk (S05-04-naming, "Resolving the
// key"). Linux hosts come later (S12-platforms), so it returns an error
// that wraps errors.ErrUnsupported, and project resolution refuses
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func CanonicalDir(string) (string, error) { return "", errCanonicalUnsupported }

var errCanonicalUnsupported = fmt.Errorf("canonical paths on linux: not supported on this host yet: %w", errors.ErrUnsupported)
