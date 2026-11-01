package secretstore

import (
	"context"
	"log/slog"
	"slices"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// check runs the checks of a call on item, in the order of the package
// comment, and returns the group the call goes to. It enforces
// SEC12-least-privilege for the store: S15-02-secret-store, "The group
// of each call", and S15-03-access-rules, rule 1.
func (s *checked) check(ctx context.Context, call storeitem.Access, item storeitem.Name, role storeitem.Role) (string, error) {
	r := refusal{call: call, item: item.String()}
	if s.openRule != "" {
		return "", s.refuse(ctx, r, s.openRule, s.openMsg)
	}
	r.group = storeitem.Group(s.team, role)
	r.itemGroup = storeitem.Group(s.team, item.Role())
	switch {
	case !role.Valid() || item.Role() != role:
		return "", s.refuse(ctx, r, storeitem.RuleGroupMismatch, "the group the call names isn't the item's group")
	case !s.holds(r.group):
		return "", s.refuse(ctx, r, storeitem.RuleGroupMismatch, s.notHeld())
	case !storeitem.Declared(s.program, item.Form(), call):
		return "", s.refuse(ctx, r, storeitem.RuleAccessUndeclared, "the access isn't declared for the program")
	}
	return r.group, nil
}

// checkList runs the checks of a List of the group of role. A list
// returns the names of every form of the role, so it must be declared
// for each of them.
func (s *checked) checkList(ctx context.Context, role storeitem.Role) (string, error) {
	r := refusal{call: storeitem.AccessList}
	if s.openRule != "" {
		return "", s.refuse(ctx, r, s.openRule, s.openMsg)
	}
	r.group = storeitem.Group(s.team, role)
	switch {
	case !role.Valid():
		return "", s.refuse(ctx, r, storeitem.RuleGroupMismatch, "the call names no role")
	case !s.holds(r.group):
		return "", s.refuse(ctx, r, storeitem.RuleGroupMismatch, s.notHeld())
	case !s.listDeclared(role):
		return "", s.refuse(ctx, r, storeitem.RuleAccessUndeclared, "the access isn't declared for the program")
	}
	return r.group, nil
}

func (s *checked) listDeclared(role storeitem.Role) bool {
	for _, f := range storeitem.Forms() {
		if f.Role() == role && !storeitem.Declared(s.program, f, storeitem.AccessList) {
			return false
		}
	}
	return true
}

// holds reports whether the process's own entitlements hold group. An
// entitlement set that can't be read holds none (S15-02-secret-store,
// "The group of each call").
func (s *checked) holds(group string) bool {
	return s.groupsRead && slices.Contains(s.groups, group)
}

func (s *checked) notHeld() string {
	if !s.groupsRead {
		return "the process's entitlements can't be read, so it holds no group"
	}
	return "the process's entitlements don't hold the group"
}

// refusal is what a refusal logs: the call, the item, and the group the
// call names and the item's group, where they apply.
type refusal struct {
	call      storeitem.Access
	item      string
	group     string
	itemGroup string
}

// refuse logs a refusal before any backend call and returns its error.
// It never logs a value.
func (s *checked) refuse(ctx context.Context, r refusal, rule storeitem.Rule, msg string) error {
	attrs := []slog.Attr{
		slog.String("program", string(s.program)),
		slog.String("call", string(r.call)),
		slog.String("item", r.item),
		slog.String("group", r.group),
		slog.String("item_group", r.itemGroup),
		slog.String("rule", string(rule)),
		slog.String("reason", msg),
	}
	s.log.LogAttrs(ctx, slog.LevelError, "secret store call refused", attrs...)
	return &Error{Rule: rule, Program: s.program, Call: r.call, Item: r.item, msg: msg}
}

// result maps the backend's status of a call to the call's error, and
// logs every failure with the call, the item, the status and the rule,
// in every build (S15-02-secret-store, "Failed store calls"). Not found
// and locked wrap ErrNotFound and ErrLocked. Any other failure is
// store-read-failed for a fetch or a list, and store-write-failed for a
// write or a delete. The CA cases of S09-06-ca-keys, "Valid CA", are
// the caller's, which knows the record: the error keeps the status.
func (s *checked) result(ctx context.Context, call storeitem.Access, item, group string, status Status) error {
	if status == StatusOK {
		return nil
	}
	e := &Error{Program: s.program, Call: call, Item: item, Status: status}
	switch {
	case status == StatusNotFound:
		e.Rule, e.err, e.msg = storeitem.RuleNotFound, ErrNotFound, "the item isn't in the store"
	case status == StatusLocked:
		e.Rule, e.err, e.msg = storeitem.RuleLocked, ErrLocked, "the store is locked"
	case call == storeitem.AccessFetch || call == storeitem.AccessList:
		e.Rule, e.msg = storeitem.RuleReadFailed, "the store call failed"
	default:
		e.Rule, e.msg = storeitem.RuleWriteFailed, "the store call failed"
	}
	s.log.LogAttrs(ctx, slog.LevelError, "secret store call failed",
		slog.String("program", string(s.program)),
		slog.String("call", string(call)),
		slog.String("item", item),
		slog.String("group", group),
		slog.Int("status", int(status)),
		slog.String("rule", string(e.Rule)))
	return e
}
