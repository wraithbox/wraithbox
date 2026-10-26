//go:build unix

package guestconn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// usersGuest is a wb-guestd of the host's version that sets the time,
// and answers EnsureUsers and RemoveUser with its answer functions, or
// OK and REMOVED for each when they are nil. It records each request.
type usersGuest struct {
	guestv1.UnimplementedGuestServiceServer

	mu      sync.Mutex
	ensure  func(*guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse
	remove  func(*guestv1.RemoveUserRequest) *guestv1.RemoveUserResponse
	ensured []*guestv1.EnsureUsersRequest
	removed []*guestv1.RemoveUserRequest
}

func (g *usersGuest) serve(t *testing.T, conn net.Conn) {
	srv := grpc.NewServer(grpc.MaxRecvMsgSize(guestd.MaxRecvMsgSize))
	guestv1.RegisterGuestServiceServer(srv, g)
	go func() { _ = srv.Serve(newOneConn(conn)) }()
	t.Cleanup(srv.Stop)
	t.Cleanup(func() { _ = conn.Close() })
}

func (*usersGuest) Hello(context.Context, *guestv1.HelloRequest) (*guestv1.HelloResponse, error) {
	return &guestv1.HelloResponse{GuestdVersion: own, ServedPackages: guestd.ServedPackages()}, nil
}

func (*usersGuest) SetTime(context.Context, *guestv1.SetTimeRequest) (*guestv1.SetTimeResponse, error) {
	return &guestv1.SetTimeResponse{}, nil
}

func (g *usersGuest) EnsureUsers(_ context.Context, req *guestv1.EnsureUsersRequest) (*guestv1.EnsureUsersResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensured = append(g.ensured, req)
	if g.ensure != nil {
		return g.ensure(req), nil
	}
	return allOK(req), nil
}

func (g *usersGuest) RemoveUser(_ context.Context, req *guestv1.RemoveUserRequest) (*guestv1.RemoveUserResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removed = append(g.removed, req)
	if g.remove != nil {
		return g.remove(req), nil
	}
	return removed(req, guestv1.RemoveResult_REMOVE_RESULT_REMOVED), nil
}

func (g *usersGuest) setEnsure(f func(*guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensure = f
}

func (g *usersGuest) setRemove(f func(*guestv1.RemoveUserRequest) *guestv1.RemoveUserResponse) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.remove = f
}

// requests returns the EnsureUsers and RemoveUser requests so far.
func (g *usersGuest) requests() ([]*guestv1.EnsureUsersRequest, []*guestv1.RemoveUserRequest) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.ensured), slices.Clone(g.removed)
}

func allOK(req *guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse {
	resp := &guestv1.EnsureUsersResponse{}
	for _, u := range req.GetUsers() {
		resp.Results = append(resp.Results, &guestv1.EnsureUserResult{Uid: u.GetUid(), Result: guestv1.EnsureResult_ENSURE_RESULT_OK})
	}
	return resp
}

func removed(req *guestv1.RemoveUserRequest, result guestv1.RemoveResult) *guestv1.RemoveUserResponse {
	resp := &guestv1.RemoveUserResponse{ProjectId: req.GetProjectId(), Uid: req.GetUid(), Result: result}
	if result == guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT {
		resp.ProcessesLeft = 2
	}
	return resp
}

// usersHarness is a linked harness whose guest is g, with n projects
// registered, and the generation of the run.
func usersHarness(t *testing.T, g *usersGuest, n int) (*harness, uint64) {
	t.Helper()
	h := newHarness(t, own, own, true)
	h.users = g
	h.link()
	h.register(n)
	return h, h.links.Status(vm.SlotWork).Generation
}

// projectID is the id of the test's project i.
func projectID(i int) string { return fmt.Sprintf("%032x", i+1) }

// register adds projects 0 to n-1.
func (h *harness) register(n int) {
	h.t.Helper()
	err := h.db.Projects(h.t.Context(), func(tx *state.ProjectTx) error {
		for i := range n {
			err := tx.Insert(state.Project{
				ID: projectID(i), Realm: "native", KeyPath: fmt.Sprintf("/p/%d", i), Name: "p",
				Placement: state.PlacementWork, URLs: []string{"https://a.example/a.git"}, Created: time.Unix(100, 0),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// rm does what `wb project rm` does to the project user records: it
// queues the removals and deletes the project in one transaction.
func (h *harness) rm(i int) {
	h.t.Helper()
	err := h.db.Projects(h.t.Context(), func(tx *state.ProjectTx) error {
		if _, err := tx.QueueUserRemovals(projectID(i), time.Unix(200, 0)); err != nil {
			return err
		}
		return tx.Delete(projectID(i))
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// reconnect drops the link, and waits until the next one has sent the
// users and the time.
func (h *harness) reconnect() {
	h.t.Helper()
	_ = h.nextGuest().Close()
	h.clock.skipTo(h.t, FirstWait)
	h.nextTime()
	h.waitState(StateLinked)
}

func (h *harness) pendingRemovals() []state.UserRemoval {
	h.t.Helper()
	rms, err := h.db.UserRemovals(h.t.Context(), "macos-work")
	if err != nil {
		h.t.Fatal(err)
	}
	return rms
}

func uidsOf(reqs []*guestv1.EnsureUsersRequest) []uint32 {
	var out []uint32
	for _, r := range reqs {
		for _, u := range r.GetUsers() {
			out = append(out, u.GetUid())
		}
	}
	return out
}

func requireSessionRule(t *testing.T, err error, rule string) {
	t.Helper()
	r, ok := errors.AsType[*SessionRefusal](err)
	if !ok || r.Rule != rule {
		t.Fatalf("err = %v, want a session refusal with rule %s", err, rule)
	}
}

// EnsureProjectUser commits the user to state.db before the guest hears
// of it, and a session start of the user passes once the guest answered
// OK (S06-03-image-updates, "Allocation").
func TestUsersCommittedBeforeSent(t *testing.T) {
	g := &usersGuest{}
	h, gen := usersHarness(t, g, 1)
	var seen []state.ProjectUser
	g.setEnsure(func(req *guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse {
		// The guest's handler reads state.db: the row is there.
		seen, _ = h.db.ProjectUsers(context.Background(), "macos-work")
		return allOK(req)
	})
	u, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(0))
	if err != nil {
		t.Fatalf("EnsureProjectUser: %v", err)
	}
	if len(seen) != 1 || seen[0].UID != u.UID || u.UID != projectuser.FirstID {
		t.Fatalf("the guest saw rows %+v for user %+v", seen, u)
	}
	ensured, _ := g.requests()
	got := ensured[len(ensured)-1].GetUsers()
	if len(got) != 1 || !proto.Equal(got[0], protoUser(u)) {
		t.Fatalf("sent %v", got)
	}
	if err := h.links.CheckUser(vm.SlotWork, gen, u.UID); err != nil {
		t.Fatalf("CheckUser: %v", err)
	}
	requireSessionRule(t, h.links.CheckUser(vm.SlotWork, gen+1, u.UID), RuleSessionNoLink)
	requireSessionRule(t, h.links.CheckUser(vm.SlotWork, gen, u.UID+1), RuleSessionUserPending)
}

// At each connection the link sends every user of the data disk, and
// none of a removed project (S06-03-image-updates, "Updates").
func TestUsersSentAtEachConnection(t *testing.T) {
	g := &usersGuest{}
	h, gen := usersHarness(t, g, 3)
	for i := range 3 {
		if _, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(i)); err != nil {
			t.Fatal(err)
		}
	}
	h.rm(1)
	h.reconnect()
	ensured, rms := g.requests()
	if got := uidsOf(ensured[len(ensured)-1:]); !slices.Equal(got, []uint32{10000, 10002}) {
		t.Fatalf("users at the connection %v, want 10000 and 10002", got)
	}
	if len(rms) != 1 || rms[0].GetProjectId() != projectID(1) || rms[0].GetUid() != 10001 {
		t.Fatalf("removals %v", rms)
	}
	if left := h.pendingRemovals(); len(left) != 0 {
		t.Fatalf("removals left %v", left)
	}
	for _, uid := range []uint32{10000, 10002} {
		if err := h.links.CheckUser(vm.SlotWork, gen, uid); err != nil {
			t.Fatalf("CheckUser(%d): %v", uid, err)
		}
	}
}

// A list over wb-guestd's receive cap goes in requests that each fit it
// and MaxUsersPerRequest, and together hold every user.
func TestUsersSplit(t *testing.T) {
	const n = 600
	g := &usersGuest{}
	h, _ := usersHarness(t, g, n)
	for i := range n {
		if _, _, err := h.db.ProjectUser(t.Context(), projectID(i), "macos-work"); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := g.requests()
	h.reconnect()
	ensured, _ := g.requests()
	ensured = ensured[len(before):]
	total := 0
	for _, req := range ensured {
		size := proto.Size(req)
		if size > guestd.MaxRecvMsgSize || len(req.GetUsers()) > guestlink.MaxUsersPerRequest {
			t.Fatalf("a request of %d users and %d bytes", len(req.GetUsers()), size)
		}
		total += size
	}
	if total <= guestd.MaxRecvMsgSize {
		t.Fatalf("%d bytes in all, want a list over the cap", total)
	}
	got := uidsOf(ensured)
	if len(got) != n || got[0] != 10000 || got[n-1] != 10000+n-1 {
		t.Fatalf("%d users sent", len(got))
	}
}

// An answer outside the contract is dropped whole with its rule and
// logged, and the users of its request are refused for the generation
// (SEC11-root-gains-nothing).
func TestUsersReplyInvalid(t *testing.T) {
	cases := map[string]func(*guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse{
		"unknown UID": func(req *guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse {
			resp := allOK(req)
			resp.Results[0].Uid = 99999
			return resp
		},
		"duplicate UID": func(req *guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse {
			resp := allOK(req)
			resp.Results = append(resp.Results, resp.Results[0])
			return resp
		},
		"bad rule": func(req *guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse {
			return &guestv1.EnsureUsersResponse{Results: []*guestv1.EnsureUserResult{{
				Uid: req.GetUsers()[0].GetUid(), Result: guestv1.EnsureResult_ENSURE_RESULT_REFUSED, Rule: "Bad\x1b[2J",
			}}}
		},
		"oversized": func(req *guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse {
			resp := allOK(req)
			resp.Results[0].Rule = strings.Repeat("a", guestlink.UsersMaxRecvMsgSize)
			return resp
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			g := &usersGuest{ensure: answer}
			h, gen := usersHarness(t, g, 1)
			_, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(0))
			requireSessionRule(t, err, guestlink.RuleUsersReplyInvalid)
			if recs := h.logs.withRule(t, guestlink.RuleUsersReplyInvalid); len(recs) == 0 {
				t.Fatal("no record of the dropped answer")
			}
			for _, rec := range h.logs.records(t) {
				if strings.ContainsRune(fmt.Sprint(rec), 0x1b) {
					t.Fatalf("log record %v holds a raw control byte", rec)
				}
			}
		})
	}
}

// A user the guest refused stays refused while the generation runs,
// even when a later answer is OK, and the next generation asks again.
func TestUserRefusedUntilNextGeneration(t *testing.T) {
	g := &usersGuest{ensure: func(req *guestv1.EnsureUsersRequest) *guestv1.EnsureUsersResponse {
		return &guestv1.EnsureUsersResponse{Results: []*guestv1.EnsureUserResult{{
			Uid: req.GetUsers()[0].GetUid(), Result: guestv1.EnsureResult_ENSURE_RESULT_REFUSED, Rule: "user-exists",
		}}}
	}}
	h, gen := usersHarness(t, g, 1)
	u, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(0))
	requireSessionRule(t, err, guestlink.RuleUserRefused)
	recs := h.logs.withRule(t, guestlink.RuleUserRefused)
	if len(recs) == 0 || recs[0]["guest_rule"] != "user-exists" {
		t.Fatalf("records %v, want the guest's rule logged", recs)
	}
	g.setEnsure(nil)
	h.reconnect()
	requireSessionRule(t, h.links.CheckUser(vm.SlotWork, gen, u.UID), guestlink.RuleUserRefused)

	if err := h.manager.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	h.waitState(StateDown)
	h.start()
	h.clock.skipTo(t, FirstWait)
	h.nextTime()
	s := h.waitState(StateLinked)
	if err := h.links.CheckUser(vm.SlotWork, s.Generation, u.UID); err != nil || s.Generation == gen {
		t.Fatalf("generation %d: CheckUser = %v", s.Generation, err)
	}
}

// C04-projects: `wb project rm` with the VM stopped, then a restart of
// wb-hostd and a start of the VM, sends the removal, and the REMOVED
// answer clears it.
func TestC04RemovalAfterRestart(t *testing.T) {
	g := &usersGuest{}
	h, gen := usersHarness(t, g, 1)
	u, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.manager.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	h.waitState(StateDown)
	h.rm(0)
	h.links.SendUserRemovals() // no VM runs: nothing to send now
	if _, rms := g.requests(); len(rms) != 0 {
		t.Fatalf("removals sent to a stopped VM: %v", rms)
	}

	// wb-hostd restarts: new links over the same state.db.
	h.links.Close()
	ls, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.links = ls
	h.start()
	h.clock.skipTo(t, FirstWait)
	h.nextTime()
	h.waitState(StateLinked)
	_, rms := g.requests()
	if len(rms) != 1 || rms[0].GetProjectId() != projectID(0) || rms[0].GetUid() != u.UID ||
		rms[0].GetGid() != u.GID || rms[0].GetHomePath() != u.HomePath {
		t.Fatalf("removals %v, want the one of %+v", rms, u)
	}
	if left := h.pendingRemovals(); len(left) != 0 {
		t.Fatalf("removals left %v", left)
	}
}

// C04-projects: a removal answer for another project is dropped, and
// both removals stay queued.
func TestC04RemovalReplyForOtherProject(t *testing.T) {
	g := &usersGuest{}
	h, gen := usersHarness(t, g, 2)
	for i := range 2 {
		if _, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(i)); err != nil {
			t.Fatal(err)
		}
	}
	g.setRemove(func(req *guestv1.RemoveUserRequest) *guestv1.RemoveUserResponse {
		resp := removed(req, guestv1.RemoveResult_REMOVE_RESULT_REMOVED)
		if req.GetProjectId() == projectID(0) {
			resp.ProjectId = projectID(1)
		}
		return resp
	})
	h.rm(0)
	h.rm(1)
	h.reconnect()
	left := h.pendingRemovals()
	if len(left) != 1 || left[0].ProjectID != projectID(0) {
		t.Fatalf("removals left %v, want project 0's", left)
	}
	if recs := h.logs.withRule(t, guestlink.RuleUsersReplyInvalid); len(recs) == 0 {
		t.Fatal("no record of the dropped answer")
	}
}

// C04-projects: a "processes left" answer keeps the removal, and the
// next connection sends it again. A removal queued while the VM runs is
// sent at once.
func TestC04RemovalProcessesLeft(t *testing.T) {
	g := &usersGuest{remove: func(req *guestv1.RemoveUserRequest) *guestv1.RemoveUserResponse {
		return removed(req, guestv1.RemoveResult_REMOVE_RESULT_PROCESSES_LEFT)
	}}
	h, gen := usersHarness(t, g, 1)
	if _, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(0)); err != nil {
		t.Fatal(err)
	}
	h.rm(0)
	h.links.SendUserRemovals()
	waitFor(t, func() bool { _, rms := g.requests(); return len(rms) == 1 })
	if left := h.pendingRemovals(); len(left) != 1 {
		t.Fatalf("removals left %v, want it kept", left)
	}
	if recs := h.logs.withRule(t, guestlink.RuleUserProcessesLeft); len(recs) != 1 {
		t.Fatalf("%d processes-left records, want 1", len(recs))
	}
	g.setRemove(nil)
	h.reconnect()
	if _, rms := g.requests(); len(rms) != 2 {
		t.Fatalf("%d removals sent, want it again at the connection", len(rms))
	}
	if left := h.pendingRemovals(); len(left) != 0 {
		t.Fatalf("removals left %v", left)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

// Without a link, or without a registered project, EnsureProjectUser
// refuses with a rule, and allocates nothing for a missing project.
func TestEnsureProjectUserRefusals(t *testing.T) {
	g := &usersGuest{}
	h, gen := usersHarness(t, g, 0)
	_, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen+1, projectID(0))
	requireSessionRule(t, err, RuleSessionNoLink)
	_, err = h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(0))
	requireSessionRule(t, err, RuleUsersRecord)
	if users, _ := h.db.ProjectUsers(t.Context(), "macos-work"); len(users) != 0 {
		t.Fatalf("users %v", users)
	}
}
