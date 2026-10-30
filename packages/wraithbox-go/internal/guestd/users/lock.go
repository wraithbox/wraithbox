package users

import (
	"context"
	"log/slog"
)

// The rules of a refused unlock, kill or removal.
const (
	// RuleUnknown: the request names no project user wb-guestd made or
	// checked since it started, with the request's project, or its
	// values differ from the user's record.
	RuleUnknown = "project-user-unknown"
	// RuleRemovalRunning: a removal of the user is running.
	RuleRemovalRunning = "project-user-removal-running"
)

// locks is the lock state of the project users, in memory only
// (S13-01-process-hardening, "Locked users"; SEC08-proj-isolation).
// Every UID is locked unless Unlock unlocked it, so a new wb-guestd
// starts with every user locked and needs no stored state to fail
// closed. Its own mutex keeps a check from waiting on a running
// request.
type locks struct {
	unlocked map[uint32]bool
	// removing holds each UID whose removal runs.
	removing map[uint32]bool
}

// Locked reports whether uid is locked. While it is, wb-guestd starts
// nothing as the user except the child of its removal, and never runs
// `launchctl print` or `launchctl asuser` against it
// (S13-01-process-hardening, "Locked users").
func (m *Manager) Locked(uid uint32) bool {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	return !m.locks.unlocked[uid]
}

// Lock locks uid, for a kill or a removal. reason names it in the log.
func (m *Manager) Lock(ctx context.Context, uid uint32, reason string) {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	delete(m.locks.unlocked, uid)
	m.log.LogAttrs(ctx, slog.LevelInfo, "project user locked",
		slog.Uint64("uid", uint64(uid)), slog.String("reason", reason))
}

// Unlock unlocks uid for a session start, and returns the rule that
// refuses it, or "". It refuses a user whose removal runs, with
// RuleRemovalRunning, and one wb-guestd hasn't ensured since it
// started, with RuleUnknown. A refused user stays locked.
func (m *Manager) Unlock(ctx context.Context, uid uint32) string {
	_, known := m.Known(uid)
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	rule := ""
	switch {
	case m.locks.removing[uid]:
		rule = RuleRemovalRunning
	case !known:
		rule = RuleUnknown
	}
	if rule != "" {
		m.log.LogAttrs(ctx, slog.LevelWarn, "project user unlock refused",
			slog.Uint64("uid", uint64(uid)), slog.String("rule", rule), slog.String("decision", "refuse"))
		return rule
	}
	m.locks.unlocked[uid] = true
	m.log.LogAttrs(ctx, slog.LevelInfo, "project user unlocked",
		slog.Uint64("uid", uint64(uid)), slog.String("decision", "unlock"))
	return ""
}

// removing reports whether a removal of uid runs.
func (m *Manager) removing(uid uint32) bool {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	return m.locks.removing[uid]
}

// startRemoval marks uid's removal as running and locks the user, or
// reports false when one runs already.
func (m *Manager) startRemoval(uid uint32) bool {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	if m.locks.removing[uid] {
		return false
	}
	m.locks.removing[uid] = true
	delete(m.locks.unlocked, uid)
	return true
}

// endRemoval marks uid's removal as done.
func (m *Manager) endRemoval(uid uint32) {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	delete(m.locks.removing, uid)
}
