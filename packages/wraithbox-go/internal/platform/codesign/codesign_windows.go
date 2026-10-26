package codesign

import (
	"context"
	"errors"
	"fmt"
)

// ReadTeam returns an error that wraps errors.ErrUnsupported: Windows
// programs have no team signature of this kind
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func ReadTeam(context.Context, string) (string, error) {
	return "", fmt.Errorf("codesign: team signatures are macOS only: %w", errors.ErrUnsupported)
}

// ReadProfile returns an error that wraps errors.ErrUnsupported:
// provisioning profiles are macOS only.
func ReadProfile(context.Context, string) (Profile, error) {
	return Profile{}, fmt.Errorf("codesign: provisioning profiles are macOS only: %w", errors.ErrUnsupported)
}
