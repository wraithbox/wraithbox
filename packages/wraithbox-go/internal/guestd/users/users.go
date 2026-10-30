// Package users makes the project users of an EnsureUsers request exist
// in the guest, with their groups and homes, or refuses each one with a
// rule (S06-03-image-updates, "Creation"; S06-05-disks, "Owners and
// modes"; SEC08-proj-isolation). wb-hostd allocates every value. This
// package only checks them, compares them with what the guest's
// directory and the data volume hold, and writes what is missing.
//
// Guest root can change any record or home between two requests, so
// every value it reads back is guest input: a record or a home that
// differs from the request is refused, never changed to match.
package users

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// The rules of a refused user. Each REFUSED result carries one, and
// every refusal is logged with it.
const (
	// RuleRequestInvalid: a value of the user isn't in the form wb-hostd
	// allocates (S06-03-image-updates, "Allocation").
	RuleRequestInvalid = "project-user-request-invalid"
	// RuleConflict: the guest's directory holds a record of the user or
	// its group with another value, or another record with one of its
	// names, IDs or GeneratedUIDs. The log names the attribute.
	RuleConflict = "project-user-conflict"
	// RuleHomeOtherOwner: the home exists and another UID owns it.
	RuleHomeOtherOwner = "project-home-other-owner"
	// RuleHomeInvalid: the home is the user's, but isn't a real
	// directory with the user's GID and mode 700, or couldn't be made.
	RuleHomeInvalid = "project-home-invalid"
	// RuleIDsOwnEntry: the UID or GID owns an entry of the volume's root
	// other than the user's own home, so a write would hand it files it
	// shouldn't have.
	RuleIDsOwnEntry = "project-ids-own-entry"
	// RuleHomeRoot: the volume's root couldn't be opened or read, or
	// holds more than MaxRootEntries entries. The RootCheck's rule
	// refuses a root that fails it instead.
	RuleHomeRoot = "project-home-root"
	// RuleDirectory: the guest's directory failed a read or a write, or
	// a record read back after the writes isn't what was written.
	RuleDirectory = "project-user-directory"
	// RuleDeadline: the request ran past Deadline before this user.
	RuleDeadline = "project-user-deadline"

	// RuleRequest: the request as a whole is malformed: no user, more
	// than MaxPerRequest, or a UID, name or GeneratedUID twice. Results
	// per user would repeat a UID, which wb-hostd drops, so EnsureUsers
	// refuses it with InvalidArgument and this rule.
	RuleRequest = "guestd-users-invalid"
)

// MaxPerRequest is the most users in one request. It equals
// internal/guestlink's MaxUsersPerRequest, which the host splits by.
const MaxPerRequest = 128

// MaxRootEntries bounds the scan of the volume's root before a write.
// Guest root can fill the root, and past this many entries the user is
// refused with RuleHomeRoot.
const MaxRootEntries = 4096

// Deadline bounds one request. It is below wb-hostd's 60 s wait for the
// answer (internal/guestlink, UsersTimeout), so the users past it are
// refused one by one with RuleDeadline before the host gives up on all.
const Deadline = 50 * time.Second

// The values of every project user's records that the request doesn't
// carry.
const (
	UserRealName  = "Wraith Box project user"
	GroupRealName = "Wraith Box project group"
	Shell         = "/bin/zsh"
	// NoPassword is the Password attribute of a record with no password.
	// No shadow hash is written, and AuthenticationAuthority stays unset.
	NoPassword = "*"
	// Hidden keeps the user off the login window.
	Hidden = "1"
)

// User is one project user of a request, as wb-hostd keeps it in
// state.db (S06-03-image-updates, "Host state per project user").
type User struct {
	ProjectID string
	Name      string
	UID       uint32
	GID       uint32
	GroupName string
	UserGUID  string
	GroupGUID string
	Home      string
}

// Result is one user's answer: Rule is empty when the user exists with
// every value of the request.
type Result struct {
	UID  uint32
	Rule string
}

// Manager makes and checks the project users. Its methods are safe for
// concurrent use, and run one at a time.
type Manager struct {
	dir      guestusers.Directory
	openRoot func() (guestusers.Root, error)
	log      *slog.Logger

	mu sync.Mutex
	// known holds each user whose last result was OK, by UID.
	known map[uint32]User

	// lockMu guards locks, apart from mu, which a request holds for up
	// to Deadline.
	lockMu sync.Mutex
	locks  locks
}

// New returns a Manager that reads and writes records with dir and
// opens the volume's root with openRoot once per request.
func New(dir guestusers.Directory, openRoot func() (guestusers.Root, error), log *slog.Logger) *Manager {
	return &Manager{
		dir: dir, openRoot: openRoot, log: log, known: map[uint32]User{},
		locks: locks{unlocked: map[uint32]bool{}, removing: map[uint32]bool{}},
	}
}

// RootCheck checks the volume's root once a request has opened it,
// before any home is read or made. It returns the rule that refuses each
// user that needs the root and what it found, or an empty rule when the
// root may be used.
type RootCheck func(ctx context.Context, root guestusers.Root) (rule, found string)

// Ensure makes each user exist with exactly its values, or refuses it,
// and returns one result per user in the request's order. A refused
// user doesn't stop the others, and a user whose removal runs is
// refused with RuleRemovalRunning. Ensure leaves every user locked. The caller has checked the request
// with CheckRequest and the data mount just before, and check ties the
// root the request opens to that mount. A nil check refuses every user
// that needs the root.
func (m *Manager) Ensure(ctx context.Context, us []User, check RootCheck) []Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	q := &request{ctx: ctx, dir: m.dir, openRoot: m.openRoot, check: check}
	defer q.close()
	results := make([]Result, 0, len(us))
	for _, u := range us {
		var outcome string
		r := &refusal{rule: RuleDeadline, found: "the request's deadline passed"}
		switch {
		case m.removing(u.UID):
			r = &refusal{rule: RuleRemovalRunning, found: "a removal of the user runs"}
		case ctx.Err() == nil:
			outcome, r = q.ensure(u)
		}
		results = append(results, m.settle(ctx, u, outcome, r))
	}
	return results
}

// Refuse refuses every user with rule, the data mount check's rule when
// it failed: no user is made or checked without the volume in place
// (S06-05-disks, "Before every session start").
func (m *Manager) Refuse(ctx context.Context, us []User, rule string) []Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	results := make([]Result, 0, len(us))
	for _, u := range us {
		results = append(results, m.settle(ctx, u, "", &refusal{rule: rule, found: "the data mount check failed"}))
	}
	return results
}

// Known returns the user with uid when its last result was OK.
func (m *Manager) Known(uid uint32) (User, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.known[uid]
	return u, ok
}

// settle logs a user's decision with its rule, updates the known set,
// and returns the result.
func (m *Manager) settle(ctx context.Context, u User, outcome string, r *refusal) Result {
	attrs := []slog.Attr{
		slog.Uint64("uid", uint64(u.UID)),
		slog.String("user", cut(u.Name)),
		slog.String("project_id", cut(u.ProjectID)),
	}
	if r != nil {
		delete(m.known, u.UID)
		m.log.LogAttrs(ctx, slog.LevelWarn, "project user refused", append(attrs,
			slog.String("rule", r.rule), slog.String("decision", "refuse"),
			slog.String("attribute", r.attr), slog.String("found", r.found))...)
		return Result{UID: u.UID, Rule: r.rule}
	}
	m.known[u.UID] = u
	m.log.LogAttrs(ctx, slog.LevelInfo, "project user ensured", append(attrs,
		slog.String("decision", "accept"), slog.String("outcome", outcome))...)
	return Result{UID: u.UID}
}

// refusal is a refused user: the rule, the attribute or entry that
// differs, and what was found, for the log.
type refusal struct {
	rule  string
	attr  string
	found string
}

func conflict(attr, found string) *refusal {
	return &refusal{rule: RuleConflict, attr: attr, found: found}
}

// maxText bounds a guest value in a log line.
const maxText = 64

// cut quotes s for a log line and cuts it to maxText bytes.
func cut(s string) string {
	if len(s) > maxText {
		s = s[:maxText]
	}
	return strconv.Quote(s)
}
