// Package storetest holds an in-memory secret store backend for tests.
// It implements secretstore.Backend, below the checks, so a test opens
// it with secretstore.New and runs the real checks over it. It can claim
// a team and a set of groups, fail any call with any status, and records
// each item call with its group. No program under cmd/ may depend on it.
package storetest

import (
	"context"
	"slices"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// Call is one item call the fake saw. Item is "" for a list.
type Call struct {
	Access storeitem.Access
	Group  string
	Item   string
}

// Fake is the in-memory backend. Set its fields before secretstore.New
// reads them.
type Fake struct {
	// TeamID is the team the fake's signature claims, "" for none, and
	// TeamErr the error of reading it.
	TeamID  string
	TeamErr error
	// GroupList is the groups the fake's entitlements hold, and GroupsErr
	// the error of reading them.
	GroupList []string
	GroupsErr error
	// DialogsErr is the error of turning dialogs off.
	DialogsErr error

	mu          sync.Mutex
	dialogsOff  int
	items       map[key][]byte
	statuses    map[statusKey]secretstore.Status
	calls       []Call
	extraListed []string
}

type key struct{ group, item string }

type statusKey struct {
	access storeitem.Access
	item   string
}

// New returns a fake that claims team and holds groups.
func New(team string, groups ...string) *Fake {
	return &Fake{TeamID: team, GroupList: groups}
}

// SetStatus makes every later call of access on item return status. item
// is the full item name, or "" for a list.
func (f *Fake) SetStatus(access storeitem.Access, item string, status secretstore.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statuses == nil {
		f.statuses = map[statusKey]secretstore.Status{}
	}
	f.statuses[statusKey{access, item}] = status
}

// AddListed makes every later list return name as well, whatever its
// group, as a store could that holds an item the checks don't expect.
func (f *Fake) AddListed(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extraListed = append(f.extraListed, name)
}

// Calls returns the item calls the fake saw, in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// DialogsOffCalls returns how many times DialogsOff ran.
func (f *Fake) DialogsOffCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dialogsOff
}

// DialogsOff implements secretstore.Backend.
func (f *Fake) DialogsOff() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dialogsOff++
	return f.DialogsErr
}

// Team implements secretstore.Backend.
func (f *Fake) Team() (string, error) { return f.TeamID, f.TeamErr }

// Groups implements secretstore.Backend.
func (f *Fake) Groups() ([]string, error) {
	if f.GroupsErr != nil {
		return nil, f.GroupsErr
	}
	return slices.Clone(f.GroupList), nil
}

// record records a call and returns the status set for it.
func (f *Fake) record(access storeitem.Access, group, item string) secretstore.Status {
	f.calls = append(f.calls, Call{Access: access, Group: group, Item: item})
	return f.statuses[statusKey{access, item}]
}

// Write implements secretstore.Backend. It keeps its own copy of value.
func (f *Fake) Write(_ context.Context, group, item string, value []byte) secretstore.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st := f.record(storeitem.AccessWrite, group, item); st != secretstore.StatusOK {
		return st
	}
	if f.items == nil {
		f.items = map[key][]byte{}
	}
	f.items[key{group, item}] = slices.Clone(value)
	return secretstore.StatusOK
}

// Fetch implements secretstore.Backend. An item the fake doesn't hold is
// secretstore.StatusNotFound.
func (f *Fake) Fetch(_ context.Context, group, item string) ([]byte, secretstore.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st := f.record(storeitem.AccessFetch, group, item); st != secretstore.StatusOK {
		return nil, st
	}
	value, ok := f.items[key{group, item}]
	if !ok {
		return nil, secretstore.StatusNotFound
	}
	return slices.Clone(value), secretstore.StatusOK
}

// Delete implements secretstore.Backend. An item the fake doesn't hold
// is secretstore.StatusNotFound.
func (f *Fake) Delete(_ context.Context, group, item string) secretstore.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st := f.record(storeitem.AccessDelete, group, item); st != secretstore.StatusOK {
		return st
	}
	if _, ok := f.items[key{group, item}]; !ok {
		return secretstore.StatusNotFound
	}
	delete(f.items, key{group, item})
	return secretstore.StatusOK
}

// List implements secretstore.Backend: the names of the items in group,
// sorted, then the names of AddListed.
func (f *Fake) List(_ context.Context, group string) ([]string, secretstore.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st := f.record(storeitem.AccessList, group, ""); st != secretstore.StatusOK {
		return nil, st
	}
	var names []string
	for k := range f.items {
		if k.group == group {
			names = append(names, k.item)
		}
	}
	slices.Sort(names)
	return append(names, f.extraListed...), secretstore.StatusOK
}
