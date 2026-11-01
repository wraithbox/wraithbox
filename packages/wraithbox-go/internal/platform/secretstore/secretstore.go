// Package secretstore is the platform interface to the secret store
// (S12-01-platform-interfaces, "Secret store"; S15-02-secret-store). A
// program with store access opens it with its own compiled-in identity,
// and each call names the item and the role whose group the caller
// expects. Every call passes the checks of S15-02-secret-store and
// S15-03-access-rules before the backend runs, in this order:
//
//  1. the call that turns dialogs off failed at Open: store-dialogs-on;
//  2. the process's own signature has no team: keychain-needs-team;
//  3. the item's role isn't the role the call names, or the process's
//     entitlements don't hold that role's group or can't be read:
//     store-group-mismatch;
//  4. the access isn't declared for the program: store-access-undeclared.
//
// Each refusal and each failed call is logged with the call, the item,
// the groups and the rule, and never a value.
//
// Only wb-credset, wb-proxyd and wb-guestadmin may import this package
// (S10-01-libraries). On macOS it links a cgo shim over Security.framework
// into them. The item names, roles and declared access are in
// internal/storeitem, which every program may use.
package secretstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// Store is an open secret store, checked for one program. Every method
// refuses before the backend runs when a check fails, with an *Error
// that names the rule. A value is []byte, never string, so the caller
// can zero its copy when the work ends (S15-03-access-rules, rule 3).
type Store interface {
	// Write stores value as item, in the group of role.
	Write(ctx context.Context, item storeitem.Name, role storeitem.Role, value []byte) error
	// Fetch returns the value of item, from the group of role.
	Fetch(ctx context.Context, item storeitem.Name, role storeitem.Role) ([]byte, error)
	// Delete removes item from the group of role.
	Delete(ctx context.Context, item storeitem.Name, role storeitem.Role) error
	// List returns the names of the items in the group of role, never a
	// value.
	List(ctx context.Context, role storeitem.Role) ([]storeitem.Name, error)
}

// Status is the result of a backend call: 0, or an OSStatus on the
// Keychain. A backend on another platform maps its errors to these.
type Status int32

// The statuses the checks act on (S15-02-secret-store, "Failed store
// calls").
const (
	StatusOK Status = 0
	// StatusUnimplemented is errSecUnimplemented: the backend has no such
	// call yet.
	StatusUnimplemented Status = -4
	// StatusNotFound is errSecItemNotFound.
	StatusNotFound Status = -25300
	// StatusLocked is errSecInteractionNotAllowed, which a locked
	// Keychain returns with dialogs off.
	StatusLocked Status = -25308
	// StatusMissingEntitlement is errSecMissingEntitlement.
	StatusMissingEntitlement Status = -34018
)

func (s Status) String() string { return strconv.Itoa(int(s)) }

// Backend is a secret store below the checks: the Keychain on macOS, and
// the fake of secretstore/storetest in tests. Each item call names the
// access group it goes to, which the checks chose, and the full item
// name. A backend never decides whether a call is allowed.
type Backend interface {
	// DialogsOff turns the store's dialogs off for the whole process. It
	// runs once, first, at Open.
	DialogsOff() error
	// Team returns the team of the process's own code signature, or ""
	// when it has none. It runs once, at Open.
	Team() (string, error)
	// Groups returns the access groups the process's own entitlements
	// hold. It runs once, at Open. An error means they can't be read,
	// and the process holds no group.
	Groups() ([]string, error)

	Write(ctx context.Context, group, item string, value []byte) Status
	Fetch(ctx context.Context, group, item string) ([]byte, Status)
	Delete(ctx context.Context, group, item string) Status
	List(ctx context.Context, group string) ([]string, Status)
}

// ErrNotFound is wrapped by the error of a call for an item that isn't in
// the store. The caller refuses its work with its own rule, as a session
// start does for a missing binding.
var ErrNotFound = errors.New("secretstore: item not found")

// ErrLocked is wrapped by the error of a call while the store is locked.
// The caller waits for the unlock and says which store it waits for
// (S15-03-access-rules, rule 7).
var ErrLocked = errors.New("secretstore: store locked")

// Error is the error of every refusal and failed call. It names the
// rule, the call and the item, and holds no value.
type Error struct {
	Rule    storeitem.Rule
	Program storeitem.Program
	Call    storeitem.Access
	// Item is the full item name, or "" for List.
	Item string
	// Status is the backend's status, or StatusOK for a refusal before
	// the backend ran.
	Status Status

	msg string
	err error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("secretstore: %s %s", e.Program, e.Call)
	if e.Item != "" {
		s += " " + e.Item
	}
	if e.Status != StatusOK {
		s += " (status " + e.Status.String() + ")"
	}
	return s + ": " + string(e.Rule) + ": " + e.msg
}

// Unwrap returns ErrNotFound, ErrLocked or nil.
func (e *Error) Unwrap() error { return e.err }
