package users_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// removeFixture is a fixture with the kill loop and the child faked,
// both recording into calls with the directory's writes after them.
type removeFixture struct {
	*fixture
	calls []string
	kill  *fakeKiller
	empty *fakeEmptier
}

func newRemoveFixture() *removeFixture {
	f := &removeFixture{fixture: newFixture()}
	f.kill = &fakeKiller{calls: &f.calls}
	f.empty = &fakeEmptier{calls: &f.calls}
	return f
}

func (f *removeFixture) tools() users.RemoveTools {
	return users.RemoveTools{Kill: f.kill, Empty: f.empty, Check: rootOK}
}

func removal(u users.User) users.Removal {
	return users.Removal{ProjectID: u.ProjectID, UID: u.UID, GID: u.GID, Home: u.Home}
}

func (f *removeFixture) remove(t *testing.T, u users.User) users.RemoveResult {
	t.Helper()
	return f.m.Remove(t.Context(), removal(u), f.tools())
}

// retired is what a removal writes for u, in order.
func retired(u users.User) []string {
	return []string{
		"disable " + u.Name,
		"user " + u.Name + " IsHidden=1",
		"user " + u.Name + " UserShell=/usr/bin/false",
	}
}

// TestRemoveKnown: a removal of a known user kills it, empties the home
// as the user with its own group, removes the home, and retires the
// record, in that order. The user leaves the known set, and Ensure
// refuses it from then on.
func TestRemoveKnown(t *testing.T) {
	f := newRemoveFixture()
	u := user(10000)
	f.wantResults(t, f.ensure(t, u), "")
	f.dir.writes = nil
	res := f.remove(t, u)
	if res.Outcome != users.Removed {
		t.Fatalf("Remove = %+v, log %s", res, f.log)
	}
	want := []string{"kill 10000", "empty 10000:10000 /private/var/wbhomes/wbp10000"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls %v, want %v", f.calls, want)
	}
	if !slices.Equal(f.root.removed, []string{u.Name}) {
		t.Errorf("unlinkat of %v", f.root.removed)
	}
	if !slices.Equal(f.dir.writes, retired(u)) {
		t.Errorf("writes %v", f.dir.writes)
	}
	if _, ok := f.dir.recs[gu.Users][u.Name]; !ok {
		t.Error("the record is gone")
	}
	if _, known := f.m.Known(u.UID); known || !f.m.Locked(u.UID) {
		t.Errorf("known %v, locked %v after the removal", known, f.m.Locked(u.UID))
	}
	f.wantResults(t, f.ensure(t, u), users.RuleConflict)

	// A second removal answers Removed again.
	f.calls = nil
	if res := f.remove(t, u); res.Outcome != users.Removed {
		t.Errorf("second Remove = %+v", res)
	}
	if slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "empty") }) {
		t.Errorf("a second removal started a child with no home: %v", f.calls)
	}
}

// TestRemoveAfterRestart: a new wb-guestd knows no user, and removes one
// whose record holds the request's IDs and home.
func TestRemoveAfterRestart(t *testing.T) {
	f := newRemoveFixture()
	u := user(10000)
	f.complete(u)
	if res := f.remove(t, u); res.Outcome != users.Removed {
		t.Fatalf("Remove = %+v, log %s", res, f.log)
	}
	if !slices.Equal(f.dir.writes, retired(u)) || len(f.root.removed) != 1 {
		t.Errorf("writes %v, removed %v", f.dir.writes, f.root.removed)
	}
}

// TestRemoveAfterDiskSwap: with no record, a home owned by the UID is
// removed, and there is no record to retire. With neither, there is
// nothing left, and the answer is Removed.
func TestRemoveAfterDiskSwap(t *testing.T) {
	f := newRemoveFixture()
	u := user(10000)
	f.home(u, 0o700)
	if res := f.remove(t, u); res.Outcome != users.Removed || len(f.root.removed) != 1 || len(f.dir.writes) != 0 {
		t.Errorf("Remove = %+v, removed %v, writes %v", res, f.root.removed, f.dir.writes)
	}
	f.calls = nil
	if res := f.remove(t, u); res.Outcome != users.Removed || len(f.calls) != 0 {
		t.Errorf("Remove of nothing = %+v, calls %v", res, f.calls)
	}
}

// TestRemoveRefuses: a removal that names no user, or another's, is
// refused with its rule, and kills nothing and starts no child.
func TestRemoveRefuses(t *testing.T) {
	u := user(10000)
	cases := map[string]struct {
		setup func(f *removeFixture)
		r     func(r users.Removal) users.Removal
		rule  string
	}{
		"project id": {r: func(r users.Removal) users.Removal { r.ProjectID = "x"; return r }, rule: users.RuleRequestInvalid},
		"uid nobody": {
			r:    func(r users.Removal) users.Removal { r.UID, r.GID = 4294967294, 4294967294; return r },
			rule: users.RuleRequestInvalid,
		},
		"gid":  {r: func(r users.Removal) users.Removal { r.GID = 10001; return r }, rule: users.RuleRequestInvalid},
		"home": {r: func(r users.Removal) users.Removal { r.Home = "/Users/admin"; return r }, rule: users.RuleRequestInvalid},
		"other project": {
			setup: func(f *removeFixture) { f.m.Ensure(context.Background(), []users.User{u}, rootOK) },
			r: func(r users.Removal) users.Removal {
				r.ProjectID = "ffffffffffffffffffffffffffffffff"
				return r
			},
			rule: users.RuleUnknown,
		},
		"record of another uid": {
			setup: func(f *removeFixture) {
				f.complete(u)
				f.dir.recs[gu.Users][u.Name][gu.AttrUniqueID] = []string{"501"}
			},
			rule: users.RuleUnknown,
		},
		"home of another uid": {
			setup: func(f *removeFixture) {
				f.root.entries[u.Name] = gu.Entry{Name: u.Name, IsDir: true, UID: 501, GID: 20, Perm: 0o700}
			},
			rule: users.RuleUnknown,
		},
		"directory error": {setup: func(f *removeFixture) { f.dir.readErr = errors.New("dscl failed") }, rule: users.RuleDirectory},
		"root check": {
			setup: func(f *removeFixture) { f.root.openErr = errors.New("no root") },
			rule:  users.RuleHomeRoot,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRemoveFixture()
			if c.setup != nil {
				c.setup(f)
			}
			r := removal(u)
			if c.r != nil {
				r = c.r(r)
			}
			f.wantRefused(t, f.m.Remove(t.Context(), r, f.tools()), c.rule)
		})
	}
}

// wantRefused checks that res refused the removal with rule, logged it,
// and killed, started and removed nothing.
func (f *removeFixture) wantRefused(t *testing.T, res users.RemoveResult, rule string) {
	t.Helper()
	if res.Outcome != users.Refused || res.Rule != rule {
		t.Errorf("Remove = %+v, want rule %s", res, rule)
	}
	if len(f.calls) != 0 || len(f.root.removed) != 0 {
		t.Errorf("calls %v, removed %v", f.calls, f.root.removed)
	}
	if !strings.Contains(f.log.String(), "rule="+rule) {
		t.Errorf("log %s", f.log)
	}
}

// TestRemoveRootCheck: a root the data mount check refuses is refused
// with the check's rule, and a removal with no check fails closed.
func TestRemoveRootCheck(t *testing.T) {
	f := newRemoveFixture()
	u := user(10000)
	f.complete(u)
	tools := f.tools()
	tools.Check = func(context.Context, gu.Root) (string, string) { return "data-mount-other", "found" }
	f.wantRefused(t, f.m.Remove(t.Context(), removal(u), tools), "data-mount-other")
	tools.Check = nil
	f.wantRefused(t, f.m.Remove(t.Context(), removal(u), tools), users.RuleHomeRoot)
}

// TestRemoveProcessesLeft: with processes left after the kill loop, no
// child starts, and the home and the record stay as they are.
func TestRemoveProcessesLeft(t *testing.T) {
	f := newRemoveFixture()
	u := user(10000)
	f.complete(u)
	f.kill.left = 3
	res := f.remove(t, u)
	if res.Outcome != users.ProcessesLeft || res.Left != 3 {
		t.Errorf("Remove = %+v", res)
	}
	if !slices.Equal(f.calls, []string{"kill 10000"}) || len(f.root.removed) != 0 || len(f.dir.writes) != 0 {
		t.Errorf("calls %v, removed %v, writes %v", f.calls, f.root.removed, f.dir.writes)
	}
	if !f.m.Locked(u.UID) {
		t.Error("the user isn't locked")
	}
}

// TestRemoveHomeChecks: a home that is a link or a file, or another
// UID's while the record is the user's, gets no child.
func TestRemoveHomeChecks(t *testing.T) {
	u := user(10000)
	for name, c := range map[string]struct {
		e    gu.Entry
		rule string
	}{
		"link":  {gu.Entry{Name: u.Name, IsLink: true, UID: u.UID, GID: u.GID}, users.RuleHomeInvalid},
		"file":  {gu.Entry{Name: u.Name, UID: u.UID, GID: u.GID, Perm: 0o700}, users.RuleHomeInvalid},
		"owner": {gu.Entry{Name: u.Name, IsDir: true, UID: 0, GID: 0, Perm: 0o700}, users.RuleHomeOtherOwner},
	} {
		f := newRemoveFixture()
		f.complete(u)
		f.root.entries[u.Name] = c.e
		res := f.remove(t, u)
		if res.Rule != c.rule || slices.ContainsFunc(f.calls, func(s string) bool { return strings.HasPrefix(s, "empty") }) {
			t.Errorf("%s: Remove = %+v, calls %v", name, res, f.calls)
		}
		if len(f.dir.writes) != 0 {
			t.Errorf("%s: retired after a refused home: %v", name, f.dir.writes)
		}
	}
}

// TestRemoveChild: a child that exits nonzero lets the removal go on,
// and the home it left is logged with RuleHomeLeft. A child that
// doesn't start, or runs past its deadline, refuses the removal before
// the record is retired.
func TestRemoveChild(t *testing.T) {
	u := user(10000)
	f := newRemoveFixture()
	f.complete(u)
	f.empty.err = exitError{}
	f.root.full = map[string]bool{u.Name: true}
	if res := f.remove(t, u); res.Outcome != users.Removed {
		t.Errorf("Remove with a nonzero exit = %+v", res)
	}
	if !strings.Contains(f.log.String(), "rule="+users.RuleHomeLeft) || len(f.dir.writes) != 3 {
		t.Errorf("writes %v, log %s", f.dir.writes, f.log)
	}

	f = newRemoveFixture()
	f.complete(u)
	f.empty.err = errors.New("fork/exec /usr/bin/find: operation not permitted")
	if res := f.remove(t, u); res.Rule != users.RuleHomeChild || len(f.dir.writes) != 0 {
		t.Errorf("Remove with a child that didn't start = %+v, writes %v", res, f.dir.writes)
	}
	if _, ok := f.root.entries[u.Name]; !ok {
		t.Error("the home is gone")
	}
}

// TestRemoveChildDeadline: the child gets ChildDeadline, and one
// stopped at it refuses the removal, even when it reports an exit.
func TestRemoveChildDeadline(t *testing.T) {
	u := user(10000)
	f := newRemoveFixture()
	f.complete(u)
	// The child is killed when its ctx is done, and reports an exit.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.empty.onRun, f.empty.err = cancel, exitError{}
	start := time.Now()
	res := f.m.Remove(ctx, removal(u), f.tools())
	if res.Rule != users.RuleHomeChild || len(f.dir.writes) != 0 {
		t.Errorf("Remove = %+v, writes %v", res, f.dir.writes)
	}
	if d := f.empty.deadline.Sub(start); d <= 0 || d > users.ChildDeadline+time.Second {
		t.Errorf("the child's deadline is %v away, want at most %v", d, users.ChildDeadline)
	}
}

// TestRemoveRetireFails: a failed retirement refuses the removal with
// RuleDirectory, so wb-hostd asks again.
func TestRemoveRetireFails(t *testing.T) {
	u := user(10000)
	f := newRemoveFixture()
	f.complete(u)
	f.dir.disableErr = errors.New("pwpolicy failed")
	if res := f.remove(t, u); res.Rule != users.RuleDirectory {
		t.Errorf("Remove = %+v", res)
	}
	f.dir.disableErr = nil
	if res := f.remove(t, u); res.Outcome != users.Removed {
		t.Errorf("Remove again = %+v", res)
	}
}

// blockingKiller is a kill loop that waits until release is closed.
type blockingKiller struct {
	started chan struct{}
	release chan struct{}
}

func (k *blockingKiller) Kill(context.Context, uint32) procs.KillResult {
	close(k.started)
	<-k.release
	return procs.KillResult{}
}

// TestRemoveRunning: while a removal of a user runs, another removal,
// an Ensure and an Unlock of it are refused with RuleRemovalRunning.
func TestRemoveRunning(t *testing.T) {
	f := newRemoveFixture()
	u := user(10000)
	f.wantResults(t, f.ensure(t, u), "")
	k := &blockingKiller{started: make(chan struct{}), release: make(chan struct{})}
	tools := f.tools()
	tools.Kill = k
	var wg sync.WaitGroup
	var first users.RemoveResult
	wg.Go(func() { first = f.m.Remove(t.Context(), removal(u), tools) })
	<-k.started
	if res := f.remove(t, u); res.Rule != users.RuleRemovalRunning {
		t.Errorf("second Remove = %+v", res)
	}
	if got := f.m.Ensure(t.Context(), []users.User{u}, rootOK); got[0].Rule != users.RuleRemovalRunning {
		t.Errorf("Ensure = %+v", got)
	}
	if rule := f.m.Unlock(t.Context(), u.UID); rule != users.RuleRemovalRunning {
		t.Errorf("Unlock = %q", rule)
	}
	close(k.release)
	wg.Wait()
	if first.Outcome != users.Removed {
		t.Errorf("first Remove = %+v", first)
	}
}

// TestLocks: every user is locked at start, only Unlock of a known user
// unlocks it, and Lock locks it again.
func TestLocks(t *testing.T) {
	f := newFixture()
	u := user(10000)
	for _, uid := range []uint32{10000, 10001, 999_999_999} {
		if !f.m.Locked(uid) {
			t.Errorf("UID %d unlocked at start", uid)
		}
	}
	if rule := f.m.Unlock(t.Context(), u.UID); rule != users.RuleUnknown || !f.m.Locked(u.UID) {
		t.Errorf("Unlock of an unknown user = %q", rule)
	}
	f.wantResults(t, f.ensure(t, u), "")
	if !f.m.Locked(u.UID) {
		t.Error("Ensure unlocked the user")
	}
	if rule := f.m.Unlock(t.Context(), u.UID); rule != "" || f.m.Locked(u.UID) {
		t.Errorf("Unlock = %q, locked %v", rule, f.m.Locked(u.UID))
	}
	f.m.Lock(t.Context(), u.UID, "kill")
	if !f.m.Locked(u.UID) {
		t.Error("Lock left the user unlocked")
	}
	if !strings.Contains(f.log.String(), "rule="+users.RuleUnknown) {
		t.Errorf("log %s", f.log)
	}
}
