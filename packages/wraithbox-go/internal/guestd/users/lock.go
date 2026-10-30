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
	// RuleKillRunning: a kill of the user is running.
	RuleKillRunning = "project-user-kill-running"
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
	// killing counts the kills of each UID that run.
	killing map[uint32]int
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

// StartKill locks uid for a kill and marks the kill as running, so
// Unlock refuses the user until EndKill. It returns the rule that
// refuses the kill, or "": RuleRemovalRunning while a removal of uid
// runs, which locked the user and runs its own kill loop.
func (m *Manager) StartKill(ctx context.Context, uid uint32) string {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	if m.locks.removing[uid] {
		m.log.LogAttrs(ctx, slog.LevelWarn, "kill request refused",
			slog.Uint64("uid", uint64(uid)), slog.String("rule", RuleRemovalRunning), slog.String("decision", "refuse"))
		return RuleRemovalRunning
	}
	m.locks.killing[uid]++
	delete(m.locks.unlocked, uid)
	m.log.LogAttrs(ctx, slog.LevelInfo, "project user locked",
		slog.Uint64("uid", uint64(uid)), slog.String("reason", "kill"))
	return ""
}

// EndKill marks one kill of uid, which StartKill started, as done. The
// user stays locked.
func (m *Manager) EndKill(uid uint32) {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	if m.locks.killing[uid]--; m.locks.killing[uid] <= 0 {
		delete(m.locks.killing, uid)
	}
}

// Unlock unlocks uid for a session start, and returns the rule that
// refuses it, or "". It refuses a user whose removal runs, with
// RuleRemovalRunning, one whose kill runs, with RuleKillRunning, and
// one wb-guestd hasn't ensured since it started, with RuleUnknown. It
// reads the known set under lockMu, which each write of it holds too,
// so a removal that ends meanwhile can't leave a removed user unlocked.
// A refused user stays locked.
func (m *Manager) Unlock(ctx context.Context, uid uint32) string {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	rule := ""
	switch {
	case m.locks.removing[uid]:
		rule = RuleRemovalRunning
	case m.locks.killing[uid] > 0:
		rule = RuleKillRunning
	case !m.isKnownLocked(uid):
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

// isKnownLocked reports whether uid is in the known set. The caller
// holds lockMu.
func (m *Manager) isKnownLocked(uid uint32) bool {
	_, ok := m.known[uid]
	return ok
}

// forget drops uid from the known set. The caller holds mu.
func (m *Manager) forget(uid uint32) {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	delete(m.known, uid)
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
