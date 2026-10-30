//go:build unix

package guestd_test

import (
	"context"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/cleanup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gp "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestprocs"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// memSubdir is a directory of a memRoot, which keeps its files in the
// root's files map as "dir/name".
type memSubdir struct {
	root *memRoot
	self gu.Entry
}

func (d *memSubdir) Self() (gu.Entry, error) { return d.self, nil }

func (d *memSubdir) prefix() string { return d.self.Name + "/" }

func (d *memSubdir) Names(int) ([]string, error) {
	var out []string
	for k := range maps.Keys(d.root.files) {
		if name, ok := strings.CutPrefix(k, d.prefix()); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (d *memSubdir) ReadFile(name string, _ int) ([]byte, gu.Entry, error) {
	data, ok := d.root.files[d.prefix()+name]
	if !ok {
		return nil, gu.Entry{}, fs.ErrNotExist
	}
	return data, gu.Entry{Name: name, Perm: 0o600}, nil
}

func (d *memSubdir) WriteFile(name string, data []byte) error {
	d.root.files[d.prefix()+name] = slices.Clone(data)
	return nil
}

func (*memSubdir) Close() error { return nil }

// memEmptier records each home it empties. A memRoot's homes hold no
// entries, so there is nothing to remove.
type memEmptier struct{ homes []string }

func (e *memEmptier) EmptyHome(_ context.Context, _, _ uint32, home string) error {
	e.homes = append(e.homes, home)
	return nil
}

// procTable is a process table: SIGKILL ends a process unless it is
// sticky, and SIGSTOP stops it and closes stopped.
type procTable struct {
	mu       sync.Mutex
	procs    []gp.Proc
	sticky   bool
	bootouts []uint32
	stopped  chan struct{}
}

func (f *procTable) add(pid int32, uid uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.procs = append(f.procs, gp.Proc{PID: pid, Start: int64(pid), UID: uid, RUID: uid, Name: "zsh", State: gp.Running})
}

func (f *procTable) ByUID(uid uint32) ([]gp.Proc, error) { return f.InRange(uid, uid) }

func (f *procTable) InRange(first, last uint32) ([]gp.Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gp.Proc
	for _, p := range f.procs {
		if p.UID >= first && p.UID <= last {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *procTable) Signal(p gp.Proc, sig gp.Signal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.procs, func(q gp.Proc) bool { return q.Key() == p.Key() })
	switch {
	case i < 0:
	case sig == gp.Kill && !f.sticky:
		f.procs = slices.Delete(f.procs, i, i+1)
	case sig == gp.Stop:
		f.procs[i].State = gp.Stopped
		if f.stopped != nil {
			close(f.stopped)
			f.stopped = nil
		}
	}
	return nil
}

func (f *procTable) Bootout(_ context.Context, uid uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bootouts = append(f.bootouts, uid)
	return nil
}

func (f *procTable) bootoutCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bootouts)
}

// instantClock moves only when a loop sleeps, so a loop's bounds take
// no real time.
type instantClock struct{ now *atomic.Int64 }

func newInstantClock() instantClock {
	c := instantClock{now: &atomic.Int64{}}
	c.now.Store(time.Unix(1_800_000_000, 0).UnixNano())
	return c
}

func (c instantClock) Now() time.Time { return time.Unix(0, c.now.Load()) }

func (c instantClock) Sleep(_ context.Context, d time.Duration) { c.now.Add(int64(d)) }

const testProject = "0123456789abcdef0123456789abcdef"

// TestUnlockUserRPC: an unknown user stays locked with RuleUnknown, and
// an ensured one unlocks.
func TestUnlockUserRPC(t *testing.T) {
	e := newUsersEnv(t, true, goodMount(testDevice))
	_, err := e.link.Client().UnlockUser(t.Context(), &guestv1.UnlockUserRequest{Uid: 10000})
	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("UnlockUser of an unknown user: %v", err)
	}
	wantErrorInfo(t, st, users.RuleUnknown)
	if _, err := e.link.EnsureUsers(t.Context(), []*guestv1.ProjectUser{projectUser(10000)}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.link.Client().UnlockUser(t.Context(), &guestv1.UnlockUserRequest{Uid: 10000}); err != nil {
		t.Errorf("UnlockUser: %v", err)
	}
}

// TestKillUserRPC: a kill ends the user's processes only, keeps the
// result as the user's record, and the host takes the answer.
func TestKillUserRPC(t *testing.T) {
	e := newUsersEnv(t, true, goodMount(testDevice))
	e.table.add(100, 10000)
	e.table.add(101, 10001)
	out, err := e.link.KillUser(t.Context(), &guestv1.KillUserRequest{KillId: 7, ProjectId: testProject, Uid: 10000}, slog.New(slog.DiscardHandler))
	if err != nil || !out.Clean || out.KillID != 7 {
		t.Fatalf("KillUser = %+v, %v", out, err)
	}
	if left, _ := e.table.ByUID(10001); len(left) != 1 {
		t.Errorf("the other user's processes: %v", left)
	}
	rec := e.record(t, 10000)
	if rec.KillID != 7 || rec.ProjectID != testProject || rec.Left != 0 {
		t.Errorf("record %+v", rec)
	}
}

// record parses the cleanup record of uid in the env's root.
func (e *usersEnv) record(t *testing.T, uid uint32) cleanup.Record {
	t.Helper()
	data, ok := e.root.files[cleanup.DirName+"/"+cleanup.FileName(uid)]
	if !ok {
		t.Fatalf("no record of %d: %v", uid, slices.Collect(maps.Keys(e.root.files)))
	}
	rec, err := cleanup.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestKillUserLeft: a process that survives every round is a result of
// processes left, in the answer and in the record.
func TestKillUserLeft(t *testing.T) {
	e := newUsersEnv(t, true, goodMount(testDevice))
	e.table.add(100, 10000)
	e.table.sticky = true
	out, err := e.link.KillUser(t.Context(), &guestv1.KillUserRequest{KillId: 8, ProjectId: testProject, Uid: 10000}, slog.New(slog.DiscardHandler))
	if err != nil || out.Clean || out.ProcessesLeft != 1 {
		t.Fatalf("KillUser = %+v, %v", out, err)
	}
	if rec := e.record(t, 10000); rec.Left != 1 || rec.KillID != 8 {
		t.Errorf("record %+v", rec)
	}
}

// TestKillUserRefused: a request out of form, or for a user wb-guestd
// knows for another project, is refused before anything is killed.
func TestKillUserRefused(t *testing.T) {
	e := newUsersEnv(t, true, goodMount(testDevice))
	if _, err := e.link.EnsureUsers(t.Context(), []*guestv1.ProjectUser{projectUser(10000)}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	e.table.add(100, 10000)
	other := strings.Repeat("f", 32)
	for name, c := range map[string]struct {
		req  *guestv1.KillUserRequest
		code codes.Code
		rule string
	}{
		"kill ID 0":     {&guestv1.KillUserRequest{ProjectId: testProject, Uid: 10000}, codes.InvalidArgument, guestd.RuleKillRequest},
		"project ID":    {&guestv1.KillUserRequest{KillId: 1, ProjectId: "ABC", Uid: 10000}, codes.InvalidArgument, guestd.RuleKillRequest},
		"system UID":    {&guestv1.KillUserRequest{KillId: 1, ProjectId: testProject, Uid: 501}, codes.InvalidArgument, guestd.RuleKillRequest},
		"other project": {&guestv1.KillUserRequest{KillId: 1, ProjectId: other, Uid: 10000}, codes.FailedPrecondition, users.RuleUnknown},
	} {
		_, err := e.link.Client().KillUser(t.Context(), c.req)
		st := status.Convert(err)
		if st.Code() != c.code {
			t.Fatalf("%s: %v, want %v", name, err, c.code)
		}
		wantErrorInfo(t, st, c.rule)
	}
	if n := e.table.bootoutCount(); n != 0 {
		t.Errorf("%d bootouts, want none", n)
	}
	if left, _ := e.table.ByUID(10000); len(left) != 1 {
		t.Errorf("the user's processes: %v", left)
	}
}

// TestKillUserRecordRefused: a kill whose record can't be kept fails
// the call with the rule, so the host never takes a result that isn't
// kept: before the data mount, and with a records' directory guest root
// left owned by a user.
func TestKillUserRecordRefused(t *testing.T) {
	req := &guestv1.KillUserRequest{KillId: 1, ProjectId: testProject, Uid: 10000}
	e := newUsersEnv(t, false, goodMount(testDevice))
	_, err := e.link.Client().KillUser(t.Context(), req)
	wantErrorInfo(t, status.Convert(err), guestd.RuleMountPending)

	e = newUsersEnv(t, true, goodMount(testDevice))
	e.root.homes[cleanup.DirName] = gu.Entry{Name: cleanup.DirName, IsDir: true, UID: 501, Perm: 0o700}
	_, err = e.link.Client().KillUser(t.Context(), req)
	wantErrorInfo(t, status.Convert(err), cleanup.RuleDir)
}

// TestRemoveUserRPC: a removal kills the user's processes, empties and
// removes the home, disables the record, and answers REMOVED, which the
// host takes. Asked again, it answers REMOVED again.
func TestRemoveUserRPC(t *testing.T) {
	e := newUsersEnv(t, true, goodMount(testDevice))
	u := projectUser(10000)
	if _, err := e.link.EnsureUsers(t.Context(), []*guestv1.ProjectUser{u}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	e.table.add(100, 10000)
	req := &guestv1.RemoveUserRequest{ProjectId: u.GetProjectId(), Uid: u.GetUid(), Gid: u.GetGid(), HomePath: u.GetHomePath()}
	for range 2 {
		out, err := e.link.RemoveUser(t.Context(), req, slog.New(slog.DiscardHandler))
		if err != nil || out.Result != guestv1.RemoveResult_REMOVE_RESULT_REMOVED {
			t.Fatalf("RemoveUser = %+v, %v", out, err)
		}
	}
	if _, ok := e.root.homes[u.GetUserName()]; ok {
		t.Error("the home is still there")
	}
	if left, _ := e.table.ByUID(10000); len(left) != 0 {
		t.Errorf("processes left: %v", left)
	}
	// The second removal finds no home, so no child runs.
	if len(e.empty.homes) != 1 || !slices.Contains(e.dir.disabled, u.GetUserName()) {
		t.Errorf("emptied %v, disabled %v", e.empty.homes, e.dir.disabled)
	}
	if _, ok := e.dir.recs[e.dir.key(gu.Users, u.GetUserName())]; !ok {
		t.Error("the record is gone, want it kept")
	}
}

// TestRemoveUserNotRemoved: with processes left, the answer says how
// many, and before the data mount the removal is refused with the
// check's rule. The host keeps the pending removal for both.
func TestRemoveUserNotRemoved(t *testing.T) {
	u := projectUser(10000)
	req := &guestv1.RemoveUserRequest{ProjectId: u.GetProjectId(), Uid: u.GetUid(), Gid: u.GetGid(), HomePath: u.GetHomePath()}
	e := newUsersEnv(t, true, goodMount(testDevice))
	if _, err := e.link.EnsureUsers(t.Context(), []*guestv1.ProjectUser{u}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	e.table.add(100, 10000)
	e.table.sticky = true
	out, err := e.link.RemoveUser(t.Context(), req, slog.New(slog.DiscardHandler))
	if err != nil || out.Result != guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT || out.ProcessesLeft != 1 {
		t.Errorf("RemoveUser = %+v, %v", out, err)
	}
	if _, ok := e.root.homes[u.GetUserName()]; !ok || len(e.empty.homes) != 0 {
		t.Error("the home was touched with processes left")
	}

	e = newUsersEnv(t, false, goodMount(testDevice))
	out, err = e.link.RemoveUser(t.Context(), req, slog.New(slog.DiscardHandler))
	if err != nil || out.Result != guestv1.RemoveResult_REMOVE_RESULT_REFUSED || out.Rule != guestd.RuleMountPending {
		t.Errorf("RemoveUser before the mount = %+v, %v", out, err)
	}
}

// TestLinkDropStops: when the host's connection ends, the stop loop
// stops the project users' processes.
func TestLinkDropStops(t *testing.T) {
	e := newUsersEnv(t, true, goodMount(testDevice))
	stopped := make(chan struct{})
	e.table.mu.Lock()
	e.table.stopped = stopped
	e.table.mu.Unlock()
	e.table.add(100, 10000)
	if err := e.link.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("no process stopped after the link dropped")
	}
}
