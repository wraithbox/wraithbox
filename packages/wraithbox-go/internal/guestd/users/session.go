package users

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// What a shell start needs of the project users
// (S06-09-session-lifecycle, "Session helper").

// The errors of Hold.
var (
	// ErrUnknown: no project user wb-guestd made or checked since it
	// started has the UID.
	ErrUnknown = errors.New("users: not a known project user")
	// ErrLocked: the user is locked (S13-01-process-hardening, "Locked
	// users").
	ErrLocked = errors.New("users: the project user is locked")
)

// PrivilegedGroups are the groups whose members can become root on
// macOS. A project user is in neither (S06-08-projects-and-sessions,
// "Standard accounts").
var PrivilegedGroups = []string{"admin", "wheel"}

// KnownByName returns the known project user named name. It reads the
// known set under lockMu, so a running request doesn't hold it up.
func (m *Manager) KnownByName(name string) (User, bool) {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	for _, u := range m.known {
		if u.Name == name {
			return u, true
		}
	}
	return User{}, false
}

// Privileged reads the groups of PrivilegedGroups from the directory,
// and reports whether u is in one: listed by name or GeneratedUID,
// through its own group nested in it, or with the group as its primary
// group. A group nested deeper isn't followed. Ensure refuses a user
// that any other record lists as a member, and this check runs again at
// each shell start (SEC12-least-privilege). A read that fails returns
// the error, and the caller refuses.
func (m *Manager) Privileged(ctx context.Context, u User) (bool, error) {
	if u.UID == 0 || u.GID == 0 {
		return true, nil
	}
	gid := strconv.FormatUint(uint64(u.GID), 10)
	for _, name := range PrivilegedGroups {
		rec, found, err := m.dir.Read(ctx, gu.Groups, name)
		if err != nil {
			return false, fmt.Errorf("users: read group %s: %w", name, err)
		}
		if !found {
			continue
		}
		if holds(rec[gu.AttrGroupMembership], u.Name) || holds(rec[gu.AttrGroupMembers], u.UserGUID) ||
			holds(rec[gu.AttrNestedGroups], u.GroupGUID) || holds(rec[gu.AttrPrimaryGroupID], gid) {
			return true, nil
		}
	}
	return false, nil
}

// holds reports whether vs has v, without case, as holder compares.
func holds(vs []string, v string) bool {
	return slices.ContainsFunc(vs, func(s string) bool { return strings.EqualFold(s, v) })
}

// Hold runs start while u is known with the same values and unlocked,
// under lockMu. So a lock, a kill or a removal of u that begins
// meanwhile waits until start returns, and its kill loop finds what
// start started (SEC08-proj-isolation). It returns ErrUnknown or
// ErrLocked without running start.
func (m *Manager) Hold(u User, start func()) error {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	if k, ok := m.known[u.UID]; !ok || k != u {
		return ErrUnknown
	}
	if !m.locks.unlocked[u.UID] {
		return ErrLocked
	}
	start()
	return nil
}
