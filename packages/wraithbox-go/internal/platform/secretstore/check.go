package secretstore

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// checked is the Store of every backend: it runs the checks before each
// backend call, and maps the backend's statuses to errors.
type checked struct {
	program storeitem.Program
	backend Backend
	log     *slog.Logger

	// openRule is the rule that refuses every call, from Open, or "".
	openRule storeitem.Rule
	openMsg  string
	team     string
	groups   []string
	// groupsRead is false when the entitlements couldn't be read: then
	// the process holds no group.
	groupsRead bool
}

// New opens backend for program, the program's own compiled-in
// identity. It turns dialogs off first, then reads the team and the
// groups of the process's own signature, each once (S15-02-secret-store,
// "The group of each call"; S15-03-access-rules, "No Keychain dialogs").
//
// New always returns a Store. When dialogs stay on or the signature has
// no team, it also returns an *Error with the rule, and every call on
// the Store is refused with that rule. The caller logs the error and
// exits. Open calls New with the platform's backend, and tests call it
// with the fake of secretstore/storetest.
func New(program storeitem.Program, backend Backend, log *slog.Logger) (Store, error) {
	s := &checked{program: program, backend: backend, log: log}
	// S15-03-access-rules, "No Keychain dialogs": before any other store
	// call, and a failure means no store call at all.
	if err := backend.DialogsOff(); err != nil {
		return s, s.refuseOpen(storeitem.RuleDialogsOn, "turning the store's dialogs off failed: "+err.Error())
	}
	s.team = s.readTeam()
	s.groups, s.groupsRead = s.readGroups()
	if s.team == "" {
		// S15-02-secret-store, "Ad-hoc signed builds".
		return s, s.refuseOpen(storeitem.RuleNeedsTeam, "the process's own signature has no team, so it has no Keychain backend")
	}
	log.LogAttrs(context.Background(), slog.LevelInfo, "secret store opened",
		slog.String("program", string(program)),
		slog.String("team", s.team),
		slog.Any("groups", s.groups),
		slog.Bool("groups_read", s.groupsRead))
	return s, nil
}

// refuseOpen makes s refuse every call with rule, logs it once, and
// returns the error of Open.
func (s *checked) refuseOpen(rule storeitem.Rule, msg string) error {
	s.openRule, s.openMsg = rule, msg
	s.log.LogAttrs(context.Background(), slog.LevelError, "secret store refused",
		slog.String("program", string(s.program)),
		slog.String("rule", string(rule)),
		slog.String("reason", msg))
	return &Error{Rule: rule, Program: s.program, Call: "open", msg: msg}
}

// readTeam returns the team of the process's own signature, or "" when
// it has none or the backend can't read it, which is logged. A value
// that isn't a team ID counts as none.
func (s *checked) readTeam() string {
	team, err := s.backend.Team()
	switch {
	case err != nil:
		s.log.LogAttrs(context.Background(), slog.LevelWarn, "secret store: the signature's team can't be read",
			slog.String("program", string(s.program)), slog.String("error", err.Error()))
		return ""
	case team != "" && !codesign.ValidTeamID(team):
		s.log.LogAttrs(context.Background(), slog.LevelWarn, "secret store: the signature's team is not a team ID",
			slog.String("program", string(s.program)), slog.String("team", strconv.QuoteToASCII(team)))
		return ""
	}
	return team
}

// readGroups returns the groups of the process's own entitlements, and
// false when they can't be read, which is logged.
func (s *checked) readGroups() ([]string, bool) {
	groups, err := s.backend.Groups()
	if err != nil {
		s.log.LogAttrs(context.Background(), slog.LevelWarn, "secret store: the entitlements can't be read, so no group is held",
			slog.String("program", string(s.program)), slog.String("error", err.Error()))
		return nil, false
	}
	return groups, true
}

func (s *checked) Write(ctx context.Context, item storeitem.Name, role storeitem.Role, value []byte) error {
	group, err := s.check(ctx, storeitem.AccessWrite, item, role)
	if err != nil {
		return err
	}
	return s.result(ctx, storeitem.AccessWrite, item.String(), group, s.backend.Write(ctx, group, item.String(), value))
}

func (s *checked) Fetch(ctx context.Context, item storeitem.Name, role storeitem.Role) ([]byte, error) {
	group, err := s.check(ctx, storeitem.AccessFetch, item, role)
	if err != nil {
		return nil, err
	}
	value, status := s.backend.Fetch(ctx, group, item.String())
	if err := s.result(ctx, storeitem.AccessFetch, item.String(), group, status); err != nil {
		clear(value)
		return nil, err
	}
	return value, nil
}

func (s *checked) Delete(ctx context.Context, item storeitem.Name, role storeitem.Role) error {
	group, err := s.check(ctx, storeitem.AccessDelete, item, role)
	if err != nil {
		return err
	}
	return s.result(ctx, storeitem.AccessDelete, item.String(), group, s.backend.Delete(ctx, group, item.String()))
}

// List returns the names in the group of role. A name the backend gives
// that doesn't parse, or has another role, fails the whole call with
// store-read-failed: only Wraith Box's own programs write the group, so
// such a name means the store isn't what the checks assume.
func (s *checked) List(ctx context.Context, role storeitem.Role) ([]storeitem.Name, error) {
	group, err := s.checkList(ctx, role)
	if err != nil {
		return nil, err
	}
	raw, status := s.backend.List(ctx, group)
	if err := s.result(ctx, storeitem.AccessList, "", group, status); err != nil {
		return nil, err
	}
	names := make([]storeitem.Name, 0, len(raw))
	for _, r := range raw {
		n, err := storeitem.Parse(r)
		switch {
		case err != nil:
			return nil, s.listNameRefused(ctx, group, r, "not an item name")
		case n.Role() != role:
			return nil, s.listNameRefused(ctx, group, r, "an item of the role "+string(n.Role()))
		}
		names = append(names, n)
	}
	return names, nil
}

// maxLoggedName bounds a name from the backend in the log.
const maxLoggedName = 160

// listNameRefused logs and returns the failure of a List whose backend
// gave raw, which isn't an item of the group. The name is quoted and
// bounded in the log.
func (s *checked) listNameRefused(ctx context.Context, group, raw, reason string) error {
	if len(raw) > maxLoggedName {
		raw = raw[:maxLoggedName]
	}
	s.log.LogAttrs(ctx, slog.LevelError, "secret store call failed",
		slog.String("program", string(s.program)),
		slog.String("call", string(storeitem.AccessList)),
		slog.String("group", group),
		slog.String("item", strconv.QuoteToASCII(raw)),
		slog.String("rule", string(storeitem.RuleReadFailed)),
		slog.String("reason", reason))
	return &Error{
		Rule: storeitem.RuleReadFailed, Program: s.program, Call: storeitem.AccessList,
		msg: "the store listed a name that isn't an item of the group " + group,
	}
}
