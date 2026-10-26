package users

import (
	"context"
	"slices"
	"strconv"
	"strings"

	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// The outcomes of a user that is ensured, for the log.
const (
	outcomeKept      = "kept"
	outcomeCreated   = "created"
	outcomeCompleted = "completed"
)

// attr is one attribute value of a record.
type attr struct{ name, value string }

// groupAttrs is the group record of u, in the order it is written.
// GeneratedUID comes first: once the record has an ID, macOS no longer
// lets GeneratedUID change (X08-data-disk, answer 11).
func groupAttrs(u User) []attr {
	return []attr{
		{gu.AttrGeneratedUID, u.GroupGUID},
		{gu.AttrRealName, GroupRealName},
		{gu.AttrPassword, NoPassword},
		{gu.AttrPrimaryGroupID, strconv.FormatUint(uint64(u.GID), 10)},
	}
}

// userAttrs is the user record of u, in the order it is written:
// GeneratedUID first, and the IDs last, so the record is complete before
// macOS locks GeneratedUID and the home path (X08-data-disk, answer 11).
// There is no AuthenticationAuthority and no shadow hash, so the user
// has no password (SEC12-least-privilege).
func userAttrs(u User) []attr {
	return []attr{
		{gu.AttrGeneratedUID, u.UserGUID},
		{gu.AttrHome, u.Home},
		{gu.AttrShell, Shell},
		{gu.AttrRealName, UserRealName},
		{gu.AttrPassword, NoPassword},
		{gu.AttrIsHidden, Hidden},
		{gu.AttrPrimaryGroupID, strconv.FormatUint(uint64(u.GID), 10)},
		{gu.AttrUniqueID, strconv.FormatUint(uint64(u.UID), 10)},
	}
}

// The attributes of every record listed for the checks against other
// records.
var (
	userListAttrs  = []string{gu.AttrRecordName, gu.AttrUniqueID, gu.AttrPrimaryGroupID, gu.AttrGeneratedUID}
	groupListAttrs = []string{
		gu.AttrRecordName, gu.AttrPrimaryGroupID, gu.AttrGeneratedUID,
		gu.AttrGroupMembership, gu.AttrGroupMembers, gu.AttrNestedGroups,
	}
)

// refusedAttrs are the attributes a project user's or group's own
// record never has: a password hash or its reference, and any member
// or nested group (SEC12-least-privilege). A record with one is a
// conflict, never completed.
var refusedAttrs = []string{
	gu.AttrAuthAuthority, gu.AttrShadowHash,
	gu.AttrGroupMembership, gu.AttrGroupMembers, gu.AttrNestedGroups,
}

// plan is what a user needs written: the missing attributes of each
// record, and whether the home is missing.
type plan struct {
	group  []attr
	user   []attr
	mkHome bool
	// fresh is set when neither record existed.
	fresh bool
}

func (p plan) empty() bool { return len(p.group) == 0 && len(p.user) == 0 && !p.mkHome }

func (p plan) outcome() string {
	switch {
	case p.empty():
		return outcomeKept
	case p.fresh:
		return outcomeCreated
	}
	return outcomeCompleted
}

// request holds what one request reads once: the lists of records, the
// volume's root and its entries.
type request struct {
	ctx      context.Context
	dir      gu.Directory
	openRoot func() (gu.Root, error)
	check    RootCheck

	listed bool
	users  []gu.Record
	groups []gu.Record

	opened bool
	root   gu.Root
	// rootRule and rootFound refuse every user that needs the root when
	// it couldn't be opened or failed the check.
	rootRule  string
	rootFound string

	scanned bool
	entries []gu.Entry
	scanErr error
}

// ensure decides one user and makes what it lacks. Every check runs
// before the first write: a refused user has nothing written.
func (q *request) ensure(u User) (string, *refusal) {
	if r := validate(u); r != nil {
		return "", r
	}
	p, r := q.plan(u)
	if r != nil {
		return "", r
	}
	if p.empty() {
		return outcomeKept, nil
	}
	if r := q.idsFree(u); r != nil {
		return "", r
	}
	if r := q.apply(u, p); r != nil {
		return "", r
	}
	if r := q.verify(u); r != nil {
		return "", r
	}
	return p.outcome(), nil
}

// plan compares u with the directory and the volume, and returns what
// is missing, or the refusal of the first value that differs.
func (q *request) plan(u User) (plan, *refusal) {
	var p plan
	gFound, uFound := false, false
	var r *refusal
	if p.group, gFound, r = q.record(gu.Groups, u.GroupName, groupAttrs(u)); r != nil {
		return p, r
	}
	if p.user, uFound, r = q.record(gu.Users, u.Name, userAttrs(u)); r != nil {
		return p, r
	}
	p.fresh = !gFound && !uFound
	if r := q.others(u); r != nil {
		return p, r
	}
	p.mkHome, r = q.home(u)
	return p, r
}

// record reads the record of kind named name and returns the
// attributes of want it lacks.
func (q *request) record(kind gu.Kind, name string, want []attr) ([]attr, bool, *refusal) {
	rec, found, err := q.dir.Read(q.ctx, kind, name)
	if err != nil {
		return nil, false, &refusal{rule: RuleDirectory, attr: kind.String(), found: err.Error()}
	}
	if !found {
		return want, false, nil
	}
	missing, r := compare(kind, rec, name, want)
	return missing, true, r
}

// compare returns the attributes of want that rec lacks. It refuses a
// record with another name or an alias, with one of refusedAttrs, or
// with a value of want that differs. A record without want's first
// attribute, its GeneratedUID, is refused too: that can't be added once
// the record has an ID (X08-data-disk, answer 11). Other attributes are
// let through: macOS adds its own, such as RecordType and
// AppleMetaNodeLocation, to every record.
func compare(kind gu.Kind, rec gu.Record, name string, want []attr) ([]attr, *refusal) {
	if got := rec[gu.AttrRecordName]; !slices.Equal(got, []string{name}) {
		return nil, conflict(kind.String()+" "+gu.AttrRecordName, values(got))
	}
	for _, a := range refusedAttrs {
		if got := rec[a]; len(got) > 0 {
			return nil, conflict(kind.String()+" "+a, values(got))
		}
	}
	var missing []attr
	for i, a := range want {
		got := rec[a.name]
		switch {
		case len(got) == 0 && i > 0:
			missing = append(missing, a)
		case !slices.Equal(got, []string{a.value}):
			return nil, conflict(kind.String()+" "+a.name, values(got))
		}
	}
	return missing, nil
}

// others refuses u when a record under another name holds one of its
// IDs or GeneratedUIDs, lists it as a member, or nests its group, as
// admin would to make it an admin (SEC12-least-privilege). Records written earlier
// in this request hold only other users' values, which CheckRequest
// keeps distinct, so the lists are read once per request.
func (q *request) others(u User) *refusal {
	if r := q.list(); r != nil {
		return r
	}
	uid := strconv.FormatUint(uint64(u.UID), 10)
	gid := strconv.FormatUint(uint64(u.GID), 10)
	if r := holder(gu.Users, q.users, u.Name, []attr{
		{gu.AttrUniqueID, uid},
		{gu.AttrPrimaryGroupID, gid},
		{gu.AttrGeneratedUID, u.UserGUID},
		{gu.AttrGeneratedUID, u.GroupGUID},
	}); r != nil {
		return r
	}
	return holder(gu.Groups, q.groups, u.GroupName, []attr{
		{gu.AttrPrimaryGroupID, gid},
		{gu.AttrGeneratedUID, u.GroupGUID},
		{gu.AttrGeneratedUID, u.UserGUID},
		{gu.AttrGroupMembership, u.Name},
		{gu.AttrGroupMembers, u.UserGUID},
		{gu.AttrNestedGroups, u.GroupGUID},
	})
}

// holder refuses the first record of recs not named own that holds one
// of the values of keys. Values compare without case. Open Directory
// may match a GeneratedUID without case (not verified), so a lowercase
// copy of a kept one must count. For the names and IDs, folding only
// refuses more.
func holder(kind gu.Kind, recs []gu.Record, own string, keys []attr) *refusal {
	for _, rec := range recs {
		if slices.Contains(rec[gu.AttrRecordName], own) {
			continue
		}
		for _, a := range keys {
			if slices.ContainsFunc(rec[a.name], func(v string) bool { return strings.EqualFold(v, a.value) }) {
				return conflict("other "+kind.String()+" "+a.name, values(rec[gu.AttrRecordName]))
			}
		}
	}
	return nil
}

// list reads every user and group record once per request.
func (q *request) list() *refusal {
	if q.listed {
		return nil
	}
	var err error
	if q.users, err = q.dir.List(q.ctx, gu.Users, userListAttrs); err != nil {
		return &refusal{rule: RuleDirectory, attr: "user list", found: err.Error()}
	}
	if q.groups, err = q.dir.List(q.ctx, gu.Groups, groupListAttrs); err != nil {
		return &refusal{rule: RuleDirectory, attr: "group list", found: err.Error()}
	}
	q.listed = true
	return nil
}

// apply writes the plan: the group, then the user, each attribute in
// order, then the home.
func (q *request) apply(u User, p plan) *refusal {
	for _, w := range []struct {
		kind  gu.Kind
		name  string
		attrs []attr
	}{{gu.Groups, u.GroupName, p.group}, {gu.Users, u.Name, p.user}} {
		for _, a := range w.attrs {
			if err := q.dir.Create(q.ctx, w.kind, w.name, a.name, a.value); err != nil {
				return &refusal{rule: RuleDirectory, attr: w.kind.String() + " " + a.name, found: err.Error()}
			}
		}
	}
	if !p.mkHome {
		return nil
	}
	if err := q.root.Mkdir(u.Name, u.UID, u.GID); err != nil {
		return &refusal{rule: RuleHomeInvalid, attr: "home", found: err.Error()}
	}
	return nil
}

// verify reads both records and the home back: each must now hold
// every value of the request.
func (q *request) verify(u User) *refusal {
	for _, c := range []struct {
		kind gu.Kind
		name string
		want []attr
	}{{gu.Groups, u.GroupName, groupAttrs(u)}, {gu.Users, u.Name, userAttrs(u)}} {
		missing, _, r := q.record(c.kind, c.name, c.want)
		if r == nil && len(missing) > 0 {
			r = &refusal{attr: c.kind.String() + " " + missing[0].name, found: "missing after the write"}
		}
		if r != nil {
			r.rule = RuleDirectory
			return r
		}
	}
	if mk, r := q.home(u); r != nil || mk {
		return &refusal{rule: RuleHomeInvalid, attr: "home", found: "missing or changed after the write"}
	}
	return nil
}

// values joins a record's values for the log, quoted and cut.
func values(vs []string) string {
	out := make([]string, 0, min(len(vs), 4))
	for i, v := range vs {
		if i == 4 {
			out = append(out, "...")
			break
		}
		out = append(out, cut(v))
	}
	return "[" + strings.Join(out, ", ") + "]"
}

func (q *request) close() {
	if q.root != nil {
		_ = q.root.Close()
	}
}
