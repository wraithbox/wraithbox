//go:build unix

package guestd_test

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/cleanup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// memDir is a local directory that starts empty and keeps every write.
type memDir struct {
	recs map[string]gu.Record
	// disabled is each user Disable disabled.
	disabled []string
}

func (d *memDir) Disable(_ context.Context, name string) error {
	d.disabled = append(d.disabled, name)
	return nil
}

func (d *memDir) key(kind gu.Kind, name string) string { return kind.String() + "/" + name }

func (d *memDir) List(context.Context, gu.Kind, []string) ([]gu.Record, error) { return nil, nil }

func (d *memDir) Read(_ context.Context, kind gu.Kind, name string) (gu.Record, bool, error) {
	rec, ok := d.recs[d.key(kind, name)]
	return rec, ok, nil
}

func (d *memDir) Create(_ context.Context, kind gu.Kind, name, attr, value string) error {
	k := d.key(kind, name)
	if d.recs[k] == nil {
		d.recs[k] = gu.Record{gu.AttrRecordName: {name}}
	}
	// Like dscl, it keeps an attribute without a standard name under
	// its native name.
	d.recs[k][gu.RecordKey(attr)] = []string{value}
	return nil
}

// memRoot is an empty volume root on mount that keeps the homes and
// directories made, and the files written in them.
type memRoot struct {
	homes map[string]gu.Entry
	files map[string][]byte
	mount guestmount.Mount
}

func (r *memRoot) Rmdir(name string) error {
	if _, ok := r.homes[name]; !ok {
		return fs.ErrNotExist
	}
	delete(r.homes, name)
	return nil
}

func (r *memRoot) OpenDir(name string) (gu.Dir, error) {
	e, ok := r.homes[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &memSubdir{root: r, self: e}, nil
}

func (r *memRoot) Stat(name string) (gu.Entry, error) {
	if e, ok := r.homes[name]; ok {
		return e, nil
	}
	return gu.Entry{}, fs.ErrNotExist
}

func (r *memRoot) Mkdir(name string, uid, gid uint32) error {
	if _, ok := r.homes[name]; ok {
		return fs.ErrExist
	}
	r.homes[name] = gu.Entry{Name: name, IsDir: true, UID: uid, GID: gid, Perm: 0o700}
	return nil
}
func (r *memRoot) Entries(int) ([]gu.Entry, error)  { return nil, nil }
func (r *memRoot) Mount() (guestmount.Mount, error) { return r.mount, nil }
func (r *memRoot) Close() error                     { return nil }

func projectUser(id uint32) *guestv1.ProjectUser {
	name := projectuser.UserName(id)
	return &guestv1.ProjectUser{
		ProjectId:         "0123456789abcdef0123456789abcdef",
		UserName:          name,
		Uid:               id,
		Gid:               id,
		GroupName:         name,
		UserGeneratedUid:  fmt.Sprintf("0F1E2D3C-4B5A-4978-8796-A%011X", id),
		GroupGeneratedUid: fmt.Sprintf("0F1E2D3C-4B5A-4978-8796-B%011X", id),
		HomePath:          projectuser.HomePath(name),
	}
}

// usersLink serves wb-guestd on a socketpair with fake volumes, a memDir
// and a memRoot on the mount MountData makes, and returns the host's
// link to it. mount runs a MountData first.
func usersLink(t *testing.T, mount bool) *guestlink.Link {
	t.Helper()
	return usersLinkOn(t, mount, goodMount(testDevice))
}

// usersLinkOn is usersLink with a memRoot that fstatfs reports on
// rootMount.
func usersLinkOn(t *testing.T, mount bool, rootMount guestmount.Mount) *guestlink.Link {
	t.Helper()
	return newUsersEnv(t, mount, rootMount).link
}

// usersEnv is a wb-guestd served on a socketpair with fakes for the
// guest, and the host's link to it.
type usersEnv struct {
	link  *guestlink.Link
	dir   *memDir
	root  *memRoot
	table *procTable
	empty *memEmptier
}

// newUsersEnv serves wb-guestd with fake volumes, a memDir, one memRoot
// on rootMount, a procTable and a memEmptier. mount runs a MountData
// first.
func newUsersEnv(t *testing.T, mount bool, rootMount guestmount.Mount) *usersEnv {
	t.Helper()
	const v = "1.2.3"
	host, guest := socketpair(t)
	dm, _, _ := newDataMount(newFakeVolumes())
	e := &usersEnv{
		dir:   &memDir{recs: map[string]gu.Record{}},
		root:  &memRoot{homes: map[string]gu.Entry{}, files: map[string][]byte{}, mount: rootMount},
		table: &procTable{},
	}
	e.empty = &memEmptier{}
	open := func() (gu.Root, error) { return e.root, nil }
	log := slog.New(slog.DiscardHandler)
	m := users.New(e.dir, open, log)
	srv := guestd.NewServer(v, log, guestd.WithDataMount(dm), guestd.WithUsers(m),
		guestd.WithProcesses(procs.New(e.table, newInstantClock(), log)), guestd.WithRemoval(e.empty, open))
	go func() { _ = srv.Serve(newOneConn(guest)) }()
	t.Cleanup(srv.Stop)
	link, err := guestlink.Hello(t.Context(), host, v, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	t.Cleanup(func() { _ = link.Close() })
	if mount {
		if _, err := link.Client().MountData(t.Context(), &guestv1.MountDataRequest{VolumeUuid: testUUID, FirstMount: true}); err != nil {
			t.Fatalf("MountData: %v", err)
		}
	}
	e.link = link
	return e
}

// TestEnsureUsersRPC: wb-hostd's own check takes the answer, with OK for
// a valid user and the rule for an invalid one.
func TestEnsureUsersRPC(t *testing.T) {
	link := usersLink(t, true)
	bad := projectUser(10001)
	bad.HomePath = "/var/root"
	got, err := link.EnsureUsers(t.Context(), []*guestv1.ProjectUser{projectUser(10000), bad}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if !got[10000].OK || got[10001].OK || got[10001].Rule != users.RuleRequestInvalid {
		t.Errorf("results %+v", got)
	}
}

// TestEnsureUsersOneBadUser: a user with one GeneratedUID for both its
// records, and two users without any, are each refused with
// project-user-request-invalid, and the valid user of the same request
// is made.
func TestEnsureUsersOneBadUser(t *testing.T) {
	link := usersLink(t, true)
	same := projectUser(10001)
	same.GroupGeneratedUid = same.UserGeneratedUid
	none, none2 := projectUser(10002), projectUser(10003)
	none.UserGeneratedUid, none.GroupGeneratedUid = "", ""
	none2.UserGeneratedUid, none2.GroupGeneratedUid = "", ""
	resp, err := link.Client().EnsureUsers(t.Context(), &guestv1.EnsureUsersRequest{
		Users: []*guestv1.ProjectUser{projectUser(10000), same, none, none2},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]string{10000: "", 10001: users.RuleRequestInvalid, 10002: users.RuleRequestInvalid, 10003: users.RuleRequestInvalid}
	if len(resp.GetResults()) != len(want) {
		t.Fatalf("results %v", resp.GetResults())
	}
	for _, r := range resp.GetResults() {
		if r.GetRule() != want[r.GetUid()] || (r.GetRule() == "") != (r.GetResult() == guestv1.EnsureResult_ENSURE_RESULT_OK) {
			t.Errorf("uid %d: %v %q, want rule %q", r.GetUid(), r.GetResult(), r.GetRule(), want[r.GetUid()])
		}
	}
}

// TestEnsureUsersBeforeMount: with no data volume mounted, every user is
// refused with the data mount check's rule, and the host takes it.
func TestEnsureUsersBeforeMount(t *testing.T) {
	link := usersLink(t, false)
	got, err := link.EnsureUsers(t.Context(), []*guestv1.ProjectUser{projectUser(10000)}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if got[10000].OK || got[10000].Rule != guestd.RuleMountPending {
		t.Errorf("results %+v", got)
	}
}

// TestEnsureUsersRootOnOtherMount: a home root that fstatfs reports on
// another file system than the mount the check passed refuses each user
// with the mount rule, as a volume swapped after the check would.
func TestEnsureUsersRootOnOtherMount(t *testing.T) {
	other := goodMount("disk9s1")
	link := usersLinkOn(t, true, other)
	got, err := link.EnsureUsers(t.Context(), []*guestv1.ProjectUser{projectUser(10000)}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if got[10000].OK || got[10000].Rule != guestd.RuleMountVolume {
		t.Errorf("results %+v", got)
	}
}

// TestEnsureUsersRequestRefused: a request with no user or with a UID
// twice is INVALID_ARGUMENT with the rule, not one result per user.
func TestEnsureUsersRequestRefused(t *testing.T) {
	link := usersLink(t, true)
	for name, req := range map[string]*guestv1.EnsureUsersRequest{
		"empty":     {},
		"UID twice": {Users: []*guestv1.ProjectUser{projectUser(10000), projectUser(10000)}},
	} {
		_, err := link.Client().EnsureUsers(t.Context(), req)
		st := status.Convert(err)
		if st.Code() != codes.InvalidArgument {
			t.Fatalf("%s: %v, want InvalidArgument", name, err)
		}
		wantErrorInfo(t, st, users.RuleRequest)
	}
}

// TestUsersRulesForm: every rule of the users fits the contract, 1 to
// 64 bytes of [a-z0-9-], and the cap matches the host's split.
func TestUsersRulesForm(t *testing.T) {
	form := regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	for _, r := range []string{
		users.RuleRequestInvalid, users.RuleConflict, users.RuleHomeOtherOwner, users.RuleHomeInvalid,
		users.RuleIDsOwnEntry, users.RuleHomeRoot, users.RuleDirectory, users.RuleDeadline, users.RuleRequest,
		users.RuleUnknown, users.RuleRemovalRunning, users.RuleKillRunning, users.RuleHomeChild, users.RuleHomeLeft,
		guestd.RuleKillRequest, cleanup.RuleDir, cleanup.RuleFile, cleanup.RuleWrite,
	} {
		if !form.MatchString(r) {
			t.Errorf("rule %q", r)
		}
	}
	if users.MaxPerRequest != guestlink.MaxUsersPerRequest {
		t.Errorf("MaxPerRequest %d, the host splits by %d", users.MaxPerRequest, guestlink.MaxUsersPerRequest)
	}
	if users.Deadline >= guestlink.UsersTimeout {
		t.Errorf("Deadline %v isn't below the host's %v", users.Deadline, guestlink.UsersTimeout)
	}
}
