//go:build !darwin || !cgo

package hwkey

import (
	"context"
	"errors"
	"fmt"
)

// unsupported is the Provider of a platform without a backend: linux,
// windows, and darwin without cgo (S12-01-platform-interfaces,
// "Unimplemented platforms fail clearly"). what names the backend that
// isn't built.
type unsupported struct{ what string }

func (u unsupported) Generate(context.Context) (Key, error) {
	return nil, u.err()
}

func (u unsupported) Restore(context.Context, []byte) (Key, error) {
	return nil, u.err()
}

func (u unsupported) err() error {
	return fmt.Errorf("hwkey: %s isn't built: %w", u.what, errors.ErrUnsupported)
}
