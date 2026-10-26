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
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// memDir is a local directory that starts empty and keeps every write.
type memDir struct{ recs map[string]gu.Record }

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
	d.recs[k][attr] = []string{value}
	return nil
}

// memRoot is an empty volume root that keeps the homes made.
type memRoot struct{ homes map[string]gu.Entry }

func (r *memRoot) Stat(name string) (gu.Entry, error) {
	if e, ok := r.homes[name]; ok {
		return e, nil
	}
	return gu.Entry{}, fs.ErrNotExist
}

func (r *memRoot) Mkdir(name string, uid, gid uint32) error {
	r.homes[name] = gu.Entry{Name: name, IsDir: true, UID: uid, GID: gid, Perm: 0o700}
	return nil
}
func (r *memRoot) Entries(int) ([]gu.Entry, error) { return nil, nil }
func (r *memRoot) Close() error                    { return nil }

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
// and a memRoot, and returns the host's link to it. mount runs a
// MountData first.
func usersLink(t *testing.T, mount bool) *guestlink.Link {
	t.Helper()
	const v = "1.2.3"
	host, guest := socketpair(t)
	dm, _, _ := newDataMount(newFakeVolumes())
	m := users.New(&memDir{recs: map[string]gu.Record{}},
		func() (gu.Root, error) { return &memRoot{homes: map[string]gu.Entry{}}, nil },
		slog.New(slog.DiscardHandler))
	srv := guestd.NewServer(v, slog.New(slog.DiscardHandler), guestd.WithDataMount(dm), guestd.WithUsers(m))
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
	return link
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
