package users_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

const project = "0123456789abcdef0123456789abcdef"

// user returns a valid project user with id, and GeneratedUIDs drawn
// from id so two users never share one.
func user(id uint32) users.User {
	name := projectuser.UserName(id)
	return users.User{
		ProjectID: project,
		Name:      name,
		UID:       id,
		GID:       id,
		GroupName: name,
		UserGUID:  guid(id, 'A'),
		GroupGUID: guid(id, 'B'),
		Home:      projectuser.HomePath(name),
	}
}

func guid(id uint32, tag byte) string {
	s := strconv.FormatUint(uint64(id), 16)
	return "0F1E2D3C-4B5A-4978-8796-" + string(tag) + strings.Repeat("0", 11-len(s)) + strings.ToUpper(s)
}

type fixture struct {
	dir  *fakeDir
	root *fakeRoot
	m    *users.Manager
	log  *bytes.Buffer
}

func newFixture() *fixture {
	f := &fixture{dir: newFakeDir(), root: newFakeRoot(), log: &bytes.Buffer{}}
	f.m = users.New(f.dir, f.root.open, slog.New(slog.NewTextHandler(f.log, nil)))
	return f
}

func (f *fixture) ensure(t *testing.T, us ...users.User) []users.Result {
	t.Helper()
	if err := users.CheckRequest(us); err != nil {
		t.Fatalf("CheckRequest: %v", err)
	}
	return f.m.Ensure(t.Context(), us)
}

// wantResults fails t unless each user's rule is the one given, "" for
// OK, and each OK user is in the known set and each refused one isn't.
func (f *fixture) wantResults(t *testing.T, got []users.Result, rules ...string) {
	t.Helper()
	if len(got) != len(rules) {
		t.Fatalf("%d results, want %d", len(got), len(rules))
	}
	for i, r := range got {
		if r.Rule != rules[i] {
			t.Errorf("result %d (uid %d): rule %q, want %q", i, r.UID, r.Rule, rules[i])
		}
		if _, known := f.m.Known(r.UID); known != (rules[i] == "") {
			t.Errorf("uid %d: known %v after rule %q", r.UID, known, r.Rule)
		}
	}
}

// complete puts u's records and home as Ensure makes them.
func (f *fixture) complete(u users.User) {
	f.dir.put(gu.Groups, u.GroupName, gu.Record{
		gu.AttrGeneratedUID:   {u.GroupGUID},
		gu.AttrRealName:       {users.GroupRealName},
		gu.AttrPassword:       {users.NoPassword},
		gu.AttrPrimaryGroupID: {strconv.FormatUint(uint64(u.GID), 10)},
	})
	f.dir.put(gu.Users, u.Name, gu.Record{
		gu.AttrGeneratedUID:     {u.UserGUID},
		gu.AttrHome:             {u.Home},
		gu.AttrShell:            {users.Shell},
		gu.AttrRealName:         {users.UserRealName},
		gu.AttrPassword:         {users.NoPassword},
		gu.AttrIsHidden:         {users.Hidden},
		gu.AttrPrimaryGroupID:   {strconv.FormatUint(uint64(u.GID), 10)},
		gu.AttrUniqueID:         {strconv.FormatUint(uint64(u.UID), 10)},
		"AppleMetaNodeLocation": {"/Local/Default"},
	})
	f.home(u, 0o700)
}

func (f *fixture) home(u users.User, perm fs.FileMode) {
	f.root.entries[u.Name] = gu.Entry{Name: u.Name, IsDir: true, UID: u.UID, GID: u.GID, Perm: perm}
}

// TestNewUser: the group, then the user, each GeneratedUID first and
// the user's IDs last, then the home with mode 700 and its owner.
func TestNewUser(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.wantResults(t, f.ensure(t, u), "")
	want := []string{
		"group wbp10000 GeneratedUID=" + u.GroupGUID,
		"group wbp10000 RealName=" + users.GroupRealName,
		"group wbp10000 Password=*",
		"group wbp10000 PrimaryGroupID=10000",
		"user wbp10000 GeneratedUID=" + u.UserGUID,
		"user wbp10000 NFSHomeDirectory=/private/var/wbhomes/wbp10000",
		"user wbp10000 UserShell=/bin/zsh",
		"user wbp10000 RealName=" + users.UserRealName,
		"user wbp10000 Password=*",
		"user wbp10000 IsHidden=1",
		"user wbp10000 PrimaryGroupID=10000",
		"user wbp10000 UniqueID=10000",
	}
	if !slices.Equal(f.dir.writes, want) {
		t.Errorf("writes\n%s\nwant\n%s", strings.Join(f.dir.writes, "\n"), strings.Join(want, "\n"))
	}
	e := f.root.entries[u.Name]
	if !slices.Equal(f.root.made, []string{u.Name}) || !e.IsDir || e.UID != 10000 || e.GID != 10000 || e.Perm != 0o700 {
		t.Errorf("home %+v, made %v", e, f.root.made)
	}
	if !strings.Contains(f.log.String(), "outcome=created") {
		t.Errorf("log: %s", f.log)
	}
	if f.root.opens != 1 || f.root.closes != 1 {
		t.Errorf("root opened %d and closed %d times", f.root.opens, f.root.closes)
	}
}

// TestSameUser: a user that exists with every value is kept, and
// nothing is written.
func TestSameUser(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.complete(u)
	f.wantResults(t, f.ensure(t, u), "")
	f.wantResults(t, f.ensure(t, u), "")
	if len(f.dir.writes) != 0 || len(f.root.made) != 0 {
		t.Errorf("writes %v, made %v", f.dir.writes, f.root.made)
	}
	if !strings.Contains(f.log.String(), "outcome=kept") {
		t.Errorf("log: %s", f.log)
	}
}

// TestRecreate: after a system disk's re-clone, the home is on the data
// volume and the records are gone. They are written again, and the home
// is kept as it is.
func TestRecreate(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.home(u, 0o700)
	f.root.entries["wbp10001"] = gu.Entry{Name: "wbp10001", IsDir: true, UID: 10001, GID: 10001, Perm: 0o700}
	f.wantResults(t, f.ensure(t, u), "")
	if len(f.dir.writes) != 12 || len(f.root.made) != 0 {
		t.Errorf("%d writes, made %v", len(f.dir.writes), f.root.made)
	}
}

// TestPartialRecord: a record whose name and GeneratedUID match, with
// some attributes missing and the others matching, is completed. Only
// the missing attributes are written.
func TestPartialRecord(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.complete(u)
	rec := f.dir.recs[gu.Users][u.Name]
	for _, a := range []string{gu.AttrShell, gu.AttrIsHidden, gu.AttrPrimaryGroupID, gu.AttrUniqueID} {
		delete(rec, a)
	}
	delete(f.dir.recs[gu.Groups][u.GroupName], gu.AttrPassword)
	f.wantResults(t, f.ensure(t, u), "")
	want := []string{
		"group wbp10000 Password=*",
		"user wbp10000 UserShell=/bin/zsh",
		"user wbp10000 IsHidden=1",
		"user wbp10000 PrimaryGroupID=10000",
		"user wbp10000 UniqueID=10000",
	}
	if !slices.Equal(f.dir.writes, want) {
		t.Errorf("writes %v, want %v", f.dir.writes, want)
	}
	if !strings.Contains(f.log.String(), "outcome=completed") {
		t.Errorf("log: %s", f.log)
	}
}

// TestMissingGroupRecord: a user whose group record is gone gets it
// back, and the user record is left as it is.
func TestMissingGroupRecord(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.complete(u)
	delete(f.dir.recs[gu.Groups], u.GroupName)
	f.wantResults(t, f.ensure(t, u), "")
	if len(f.dir.writes) != 4 || !strings.HasPrefix(f.dir.writes[0], "group wbp10000 GeneratedUID=") {
		t.Errorf("writes %v", f.dir.writes)
	}
}

// TestMissingHome: an existing user without a home gets one, mode 700.
func TestMissingHome(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.complete(u)
	delete(f.root.entries, u.Name)
	f.wantResults(t, f.ensure(t, u), "")
	if len(f.dir.writes) != 0 || !slices.Equal(f.root.made, []string{u.Name}) {
		t.Errorf("writes %v, made %v", f.dir.writes, f.root.made)
	}
}

// TestConflicts: every record that differs from the request in another
// way is refused with project-user-conflict, nothing is written, the
// log names the attribute, and the other user of the request is made.
func TestConflicts(t *testing.T) {
	u, other := user(10000), user(10001)
	userRec := func(f *fixture) gu.Record { return f.dir.recs[gu.Users][u.Name] }
	group := func(f *fixture) gu.Record { return f.dir.recs[gu.Groups][u.GroupName] }
	for _, tc := range []struct {
		name  string
		attr  string
		setup func(f *fixture)
	}{
		{"user GeneratedUID", "user GeneratedUID", func(f *fixture) { userRec(f)[gu.AttrGeneratedUID] = []string{guid(1, 'C')} }},
		{"user GeneratedUID missing", "user GeneratedUID", func(f *fixture) { delete(userRec(f), gu.AttrGeneratedUID) }},
		{"user UniqueID", "user UniqueID", func(f *fixture) { userRec(f)[gu.AttrUniqueID] = []string{"0"} }},
		{"user PrimaryGroupID admin", "user PrimaryGroupID", func(f *fixture) { userRec(f)[gu.AttrPrimaryGroupID] = []string{"80"} }},
		{"user home", "user NFSHomeDirectory", func(f *fixture) { userRec(f)[gu.AttrHome] = []string{"/var/root"} }},
		{"user shell", "user UserShell", func(f *fixture) { userRec(f)[gu.AttrShell] = []string{"/bin/sh"} }},
		{"user two values", "user RealName", func(f *fixture) { userRec(f)[gu.AttrRealName] = []string{"a", "b"} }},
		{"user password", "user AuthenticationAuthority", func(f *fixture) {
			userRec(f)[gu.AttrAuthAuthority] = []string{";ShadowHash;"}
		}},
		{"user alias", "user RecordName", func(f *fixture) { userRec(f)[gu.AttrRecordName] = []string{u.Name, "root2"} }},
		{"group GeneratedUID", "group GeneratedUID", func(f *fixture) { group(f)[gu.AttrGeneratedUID] = []string{guid(1, 'C')} }},
		{"group PrimaryGroupID", "group PrimaryGroupID", func(f *fixture) { group(f)[gu.AttrPrimaryGroupID] = []string{"0"} }},
		{"group members", "group GroupMembership", func(f *fixture) { group(f)[gu.AttrGroupMembership] = []string{"root"} }},
		{"other user has the UID", "other user UniqueID", func(f *fixture) {
			f.dir.put(gu.Users, "intruder", gu.Record{gu.AttrUniqueID: {"10000"}})
		}},
		{"other user has the GID as primary", "other user PrimaryGroupID", func(f *fixture) {
			f.dir.put(gu.Users, "intruder", gu.Record{gu.AttrPrimaryGroupID: {"10000"}})
		}},
		{"other user has the GeneratedUID", "other user GeneratedUID", func(f *fixture) {
			f.dir.put(gu.Users, "intruder", gu.Record{gu.AttrGeneratedUID: {u.UserGUID}})
		}},
		{"other group has the GID", "other group PrimaryGroupID", func(f *fixture) {
			f.dir.put(gu.Groups, "intruders", gu.Record{gu.AttrPrimaryGroupID: {"10000"}})
		}},
		{"other group has the group GeneratedUID", "other group GeneratedUID", func(f *fixture) {
			f.dir.put(gu.Groups, "intruders", gu.Record{gu.AttrGeneratedUID: {u.GroupGUID}})
		}},
		{"admin lists the user", "other group GroupMembership", func(f *fixture) {
			f.dir.put(gu.Groups, "admin", gu.Record{gu.AttrGroupMembership: {"root", u.Name}})
		}},
		{"admin lists the user's GeneratedUID", "other group GroupMembers", func(f *fixture) {
			f.dir.put(gu.Groups, "admin", gu.Record{gu.AttrGroupMembers: {u.UserGUID}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.complete(u)
			tc.setup(f)
			before := len(f.root.entries)
			f.wantResults(t, f.ensure(t, u, other), users.RuleConflict, "")
			if slices.ContainsFunc(f.dir.writes, func(w string) bool { return strings.Contains(w, " "+u.Name+" ") }) {
				t.Errorf("wrote %v, some for the refused user", f.dir.writes)
			}
			if len(f.root.made) != 1 || len(f.root.entries) != before+1 {
				t.Errorf("made %v", f.root.made)
			}
			if !strings.Contains(f.log.String(), `attribute="`+tc.attr+`"`) ||
				!strings.Contains(f.log.String(), "rule=project-user-conflict") {
				t.Errorf("log doesn't name %q: %s", tc.attr, f.log)
			}
		})
	}
}

// TestConflictNewUser: a new user whose name is free, but whose UID
// another record holds, is refused with nothing written.
func TestConflictNewUser(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.dir.put(gu.Users, "intruder", gu.Record{gu.AttrUniqueID: {"10000"}})
	f.wantResults(t, f.ensure(t, u), users.RuleConflict)
	if len(f.dir.writes) != 0 || len(f.root.made) != 0 {
		t.Errorf("writes %v, made %v", f.dir.writes, f.root.made)
	}
}

// TestHomes: a home that another UID owns, a link, a file, or a home of
// the user's with the wrong group, mode or special bits is refused to
// that user only, before any record is written.
func TestHomes(t *testing.T) {
	u, other := user(10000), user(10001)
	for _, tc := range []struct {
		name  string
		entry gu.Entry
		rule  string
	}{
		{"other owner", gu.Entry{IsDir: true, UID: 501, GID: 10000, Perm: 0o700}, users.RuleHomeOtherOwner},
		{"root owns it", gu.Entry{IsDir: true, UID: 0, GID: 0, Perm: 0o755}, users.RuleHomeOtherOwner},
		{"link", gu.Entry{IsLink: true, UID: 10000, GID: 10000, Perm: 0o755}, users.RuleHomeInvalid},
		{"file", gu.Entry{UID: 10000, GID: 10000, Perm: 0o700}, users.RuleHomeInvalid},
		{"mode 755", gu.Entry{IsDir: true, UID: 10000, GID: 10000, Perm: 0o755}, users.RuleHomeInvalid},
		{"other group", gu.Entry{IsDir: true, UID: 10000, GID: 20, Perm: 0o700}, users.RuleHomeInvalid},
		{"setgid", gu.Entry{IsDir: true, UID: 10000, GID: 10000, Perm: 0o700, Special: true}, users.RuleHomeInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			e := tc.entry
			e.Name = u.Name
			f.root.entries[u.Name] = e
			f.wantResults(t, f.ensure(t, u, other), tc.rule, "")
			if len(f.dir.writes) != 12 || !strings.Contains(f.dir.writes[0], other.Name) {
				t.Errorf("writes %v, want only the other user's", f.dir.writes)
			}
			if f.root.entries[u.Name] != e || !slices.Equal(f.root.made, []string{other.Name}) {
				t.Errorf("home changed to %+v, made %v", f.root.entries[u.Name], f.root.made)
			}
		})
	}
}

// TestHomeOfKeptUser: a user kept as it is gets its home checked too.
func TestHomeOfKeptUser(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.complete(u)
	f.home(u, 0o755)
	f.wantResults(t, f.ensure(t, u), users.RuleHomeInvalid)
}

// TestIDsOwnEntry: a UID or GID that owns another entry of the volume's
// root is refused before any write.
func TestIDsOwnEntry(t *testing.T) {
	for _, e := range []gu.Entry{
		{Name: "stray", UID: 10000, GID: 20, Perm: 0o644},
		{Name: "wbp9", IsDir: true, UID: 501, GID: 10000, Perm: 0o700},
	} {
		t.Run(e.Name, func(t *testing.T) {
			f := newFixture()
			f.root.entries[e.Name] = e
			f.wantResults(t, f.ensure(t, user(10000)), users.RuleIDsOwnEntry)
			if len(f.dir.writes) != 0 || len(f.root.made) != 0 {
				t.Errorf("writes %v, made %v", f.dir.writes, f.root.made)
			}
		})
	}
}

// TestRootRefused: a root that can't be opened, read, or that holds
// more than MaxRootEntries entries is refused before any write.
func TestRootRefused(t *testing.T) {
	for name, setup := range map[string]func(*fakeRoot){
		"open":    func(r *fakeRoot) { r.openErr = errors.New("ENOENT") },
		"entries": func(r *fakeRoot) { r.entriesErr = errors.New("EIO") },
		"over the cap": func(r *fakeRoot) {
			for i := range users.MaxRootEntries + 1 {
				n := "f" + strconv.Itoa(i)
				r.entries[n] = gu.Entry{Name: n, UID: 501}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			setup(f.root)
			f.wantResults(t, f.ensure(t, user(10000), user(10001)), users.RuleHomeRoot, users.RuleHomeRoot)
			if len(f.dir.writes) != 0 || f.root.opens != 1 {
				t.Errorf("writes %v, opens %d", f.dir.writes, f.root.opens)
			}
		})
	}
}

// TestDirectoryFailures: a failed list, read or write, or a write the
// directory didn't keep, refuses with project-user-directory.
func TestDirectoryFailures(t *testing.T) {
	for name, setup := range map[string]func(*fakeDir){
		"list":   func(d *fakeDir) { d.listErr = errors.New("DS Error") },
		"read":   func(d *fakeDir) { d.readErr = errors.New("DS Error") },
		"create": func(d *fakeDir) { d.createErr = errors.New("DS Error") },
		"dropped write": func(d *fakeDir) {
			d.drop = gu.AttrIsHidden
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			setup(f.dir)
			f.wantResults(t, f.ensure(t, user(10000)), users.RuleDirectory)
		})
	}
}

// TestLockedRecord: a record with its IDs but without a home path can't
// be completed, since macOS locks the home path once the IDs are set.
// The one write is tried, and its failure refuses the user with
// project-user-directory.
func TestLockedRecord(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.complete(u)
	delete(f.dir.recs[gu.Users][u.Name], gu.AttrHome)
	f.wantResults(t, f.ensure(t, u), users.RuleDirectory)
	if !slices.Equal(f.dir.writes, []string{"user wbp10000 NFSHomeDirectory=" + u.Home}) {
		t.Errorf("writes %v", f.dir.writes)
	}
}

// TestInvalidUser: a user whose values aren't in the allocated form is
// refused with nothing read or written, and the others go on.
func TestInvalidUser(t *testing.T) {
	for name, change := range map[string]func(*users.User){
		"project id":         func(u *users.User) { u.ProjectID = strings.ToUpper(project) },
		"uid below range":    func(u *users.User) { u.UID, u.GID, u.Name, u.GroupName = 501, 501, "wbp501", "wbp501" },
		"uid past range":     func(u *users.User) { u.UID = projectuser.LastID + 1 },
		"gid":                func(u *users.User) { u.GID = 20 },
		"name":               func(u *users.User) { u.Name = "root" },
		"group name":         func(u *users.User) { u.GroupName = "admin" },
		"user GeneratedUID":  func(u *users.User) { u.UserGUID = strings.ToLower(u.UserGUID) },
		"group GeneratedUID": func(u *users.User) { u.GroupGUID = "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF" },
		"same GeneratedUIDs": func(u *users.User) { u.GroupGUID = u.UserGUID },
		"home":               func(u *users.User) { u.Home = "/private/var/wbhomes/../root" },
		"home of another":    func(u *users.User) { u.Home = projectuser.HomePath("wbp10001") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			bad := user(10000)
			change(&bad)
			got := f.m.Ensure(t.Context(), []users.User{bad, user(10002)})
			f.wantResults(t, got, users.RuleRequestInvalid, "")
			if f.dir.reads != 4 || len(f.dir.writes) != 12 {
				t.Errorf("%d reads, %d writes: the invalid user was read or written", f.dir.reads, len(f.dir.writes))
			}
		})
	}
}

// TestCheckRequest: a request that is empty, too long, or names a UID,
// a user name or a GeneratedUID twice is refused as a whole.
func TestCheckRequest(t *testing.T) {
	long := make([]users.User, users.MaxPerRequest+1)
	for i := range long {
		long[i] = user(10000 + uint32(i))
	}
	sameName := user(10001)
	sameName.Name = "wbp10000"
	sameGUID := user(10001)
	sameGUID.GroupGUID = user(10000).UserGUID
	for name, us := range map[string][]users.User{
		"empty":             nil,
		"too long":          long,
		"UID twice":         {user(10000), user(10000)},
		"name twice":        {user(10000), sameName},
		"GeneratedUID used": {user(10000), sameGUID},
	} {
		if err := users.CheckRequest(us); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := users.CheckRequest(long[:users.MaxPerRequest]); err != nil {
		t.Errorf("MaxPerRequest users: %v", err)
	}
}

// TestRefuse: the data mount check's rule refuses every user, with no
// read or write, and drops each from the known set.
func TestRefuse(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.wantResults(t, f.ensure(t, u), "")
	writes := len(f.dir.writes)
	got := f.m.Refuse(t.Context(), []users.User{u, user(10001)}, "data-mount-flags")
	f.wantResults(t, got, "data-mount-flags", "data-mount-flags")
	if len(f.dir.writes) != writes || f.root.opens != 1 {
		t.Errorf("Refuse wrote or opened the root")
	}
	if !strings.Contains(f.log.String(), "rule=data-mount-flags") {
		t.Errorf("log: %s", f.log)
	}
}

// TestKnownDropsRefused: a user refused after an OK leaves the known
// set.
func TestKnownDropsRefused(t *testing.T) {
	f := newFixture()
	u := user(10000)
	f.wantResults(t, f.ensure(t, u), "")
	if got, ok := f.m.Known(u.UID); !ok || got != u {
		t.Fatalf("Known = %+v, %v", got, ok)
	}
	f.home(u, 0o777)
	f.wantResults(t, f.ensure(t, u), users.RuleHomeInvalid)
}

// TestDeadline: a request whose context is done refuses each user with
// project-user-deadline, and writes nothing.
func TestDeadline(t *testing.T) {
	f := newFixture()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.wantResults(t, f.m.Ensure(ctx, []users.User{user(10000)}), users.RuleDeadline)
	if len(f.dir.writes) != 0 {
		t.Errorf("writes %v", f.dir.writes)
	}
}
