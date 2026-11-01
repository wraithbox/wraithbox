package secretstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// Open isn't built on windows, whose hosts are later (S12-platforms). The
// backend there is Credential Manager (S12-01-platform-interfaces, "Secret
// store"). It returns an error that wraps errors.ErrUnsupported, and a
// Store whose every call returns the same error
// (S12-01-platform-interfaces, "Unimplemented platforms fail clearly").
func Open(storeitem.Program) (Store, error) { return unsupported{}, errUnsupported }

var errUnsupported = fmt.Errorf("secretstore: the secret store on windows: %w", errors.ErrUnsupported)

// unsupported is the Store of a platform without a backend.
type unsupported struct{}

func (unsupported) Write(context.Context, storeitem.Name, storeitem.Role, []byte) error {
	return errUnsupported
}

func (unsupported) Fetch(context.Context, storeitem.Name, storeitem.Role) ([]byte, error) {
	return nil, errUnsupported
}

func (unsupported) Delete(context.Context, storeitem.Name, storeitem.Role) error {
	return errUnsupported
}

func (unsupported) List(context.Context, storeitem.Role) ([]storeitem.Name, error) {
	return nil, errUnsupported
}
