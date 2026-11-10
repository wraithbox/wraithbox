package secretstore

import "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"

// StoreRule returns the rule of the refusal or failed call. It lets a
// caller that may not import this package read the rule through an
// interface, as internal/credset does: wb imports that package, and wb
// may not link the store (S15-02-secret-store, "`wb` doesn't hold a
// group").
func (e *Error) StoreRule() storeitem.Rule { return e.Rule }

// StoreStatus returns the backend's status of a failed call, or 0 for a
// refusal before the backend ran.
func (e *Error) StoreStatus() int32 { return int32(e.Status) }
