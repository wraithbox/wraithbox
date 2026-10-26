//go:build unix

package guestconn

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/kills"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// killsHarness is a linked harness whose guest is g and whose links
// send kills through a tracker, with n projects registered, and the
// generation of the run.
func killsHarness(t *testing.T, g *usersGuest, n int) (*harness, *kills.Tracker, uint64) {
	t.Helper()
	h := newHarness(t, own, own, true)
	h.links.Close()
	tracker := kills.New(kills.Config{Logger: slog.New(slog.NewJSONHandler(h.logs, nil))})
	h.cfg.Kills = tracker
	ls, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.links = ls
	t.Cleanup(ls.Close)
	h.users = g
	h.link()
	h.register(n)
	return h, tracker, h.links.Status(vm.SlotWork).Generation
}

// waitAdmitted waits until a session start in generation passes, or
// fails with the last refusal.
func (h *harness) waitAdmitted(generation uint64) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := h.links.CheckSessionStart(vm.SlotWork, generation)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("session start in generation %d: %v", generation, err)
		}
		time.Sleep(time.Millisecond)
	}
}

// restart stops the work VM and starts it again, and returns the new
// generation once its link sent the time.
func (h *harness) restart() uint64 {
	h.t.Helper()
	if err := h.manager.Stop(h.t.Context(), vm.SlotWork); err != nil {
		h.t.Fatal(err)
	}
	h.waitState(StateDown)
	// Drop the guest ends of the stopped generation, so that reconnect
	// closes the new one.
	for len(h.guests) > 0 {
		<-h.guests
	}
	h.start()
	h.clock.skipTo(h.t, FirstWait)
	h.nextTime()
	return h.waitState(StateLinked).Generation
}

func (g *usersGuest) kills() []*guestv1.KillUserRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.killed)
}

func (g *usersGuest) setKill(f func(*guestv1.KillUserRequest) *guestv1.KillUserResponse) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.kill = f
}

func usersOf(t *testing.T, h *harness, gen uint64, n int) []state.ProjectUser {
	t.Helper()
	var out []state.ProjectUser
	for i := range n {
		u, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, projectID(i))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, u)
	}
	return out
}

// TestFirstConnectionKills: at the first connection of a VM generation
// the link sends a kill for every project user of the VM, each with its
// own kill ID, and session starts wait until it has
// (S06-10-leftover-processes, "Kill IDs and records"). A reconnect in
// the same generation sends none again.
func TestFirstConnectionKills(t *testing.T) {
	g := &usersGuest{}
	h, _, gen := killsHarness(t, g, 3)
	h.waitAdmitted(gen)
	users := usersOf(t, h, gen, 3)
	if n := len(g.kills()); n != 0 {
		t.Fatalf("%d kills sent before any user existed", n)
	}

	release := make(chan struct{})
	g.setKill(func(req *guestv1.KillUserRequest) *guestv1.KillUserResponse {
		<-release
		return &guestv1.KillUserResponse{KillId: req.GetKillId(), ProjectId: req.GetProjectId(), Result: guestv1.KillResult_KILL_RESULT_CLEAN}
	})
	gen2 := h.restart()
	requireSessionRule(t, h.links.CheckSessionStart(vm.SlotWork, gen2), RuleSessionKillsPending)
	close(release)
	h.waitAdmitted(gen2)

	sent := g.kills()
	if len(sent) != len(users) {
		t.Fatalf("%d kills sent, want %d: %v", len(sent), len(users), sent)
	}
	ids := map[uint64]bool{}
	for i, req := range sent {
		if req.GetProjectId() != users[i].ProjectID || req.GetUid() != users[i].UID {
			t.Fatalf("kill %d is %v, want %+v", i, req, users[i])
		}
		if req.GetKillId() == 0 || ids[req.GetKillId()] {
			t.Fatalf("kill ID %d", req.GetKillId())
		}
		ids[req.GetKillId()] = true
	}
	if n := len(h.logs.withRule(t, kills.RuleClean)); n != len(users) {
		t.Fatalf("%d clean results logged, want %d", n, len(users))
	}

	h.reconnect()
	h.waitAdmitted(gen2)
	if n := len(g.kills()); n != len(users) {
		t.Fatalf("%d kills after a reconnect, want %d", n, len(users))
	}
}

// TestProcessesLeftRefusesSessions: a first-connection kill that leaves
// processes refuses every session start and every save in the VM
// generation, and only the next generation clears it.
func TestProcessesLeftRefusesSessions(t *testing.T) {
	g := &usersGuest{}
	h, tracker, gen := killsHarness(t, g, 2)
	h.waitAdmitted(gen)
	usersOf(t, h, gen, 2)
	g.setKill(func(req *guestv1.KillUserRequest) *guestv1.KillUserResponse {
		resp := &guestv1.KillUserResponse{KillId: req.GetKillId(), ProjectId: req.GetProjectId(), Result: guestv1.KillResult_KILL_RESULT_CLEAN}
		if req.GetProjectId() == projectID(1) {
			resp.Result, resp.ProcessesLeft = guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT, 5
		}
		return resp
	})
	gen2 := h.restart()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := h.links.CheckSessionStart(vm.SlotWork, gen2)
		if r, ok := errors.AsType[*SessionRefusal](err); ok && r.Rule == kills.RuleVMProcessesLeft {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session start = %v, want %s", err, kills.RuleVMProcessesLeft)
		}
		time.Sleep(time.Millisecond)
	}
	if r := tracker.CheckSave(vm.SlotWork, gen2); r == nil || r.Rule != kills.RuleVMProcessesLeft {
		t.Fatalf("save = %v", r)
	}
	if !tracker.ColdBootNext(vm.SlotWork) {
		t.Fatal("the next start isn't a cold boot")
	}

	g.setKill(nil)
	gen3 := h.restart()
	h.waitAdmitted(gen3)
	if tracker.ColdBootNext(vm.SlotWork) {
		t.Fatal("generation 3 still asks for a cold boot")
	}
}

// TestKillUser: KillUser sends a kill of one project user, and a session
// of the project is refused while it is pending. A kill for a generation
// that doesn't run isn't sent.
func TestKillUser(t *testing.T) {
	g := &usersGuest{}
	h, tracker, gen := killsHarness(t, g, 1)
	h.waitAdmitted(gen)
	u := usersOf(t, h, gen, 1)[0]

	release := make(chan struct{})
	g.setKill(func(req *guestv1.KillUserRequest) *guestv1.KillUserResponse {
		<-release
		return &guestv1.KillUserResponse{KillId: req.GetKillId(), ProjectId: req.GetProjectId(), Result: guestv1.KillResult_KILL_RESULT_CLEAN}
	})
	done := make(chan error, 1)
	go func() { done <- h.links.KillUser(t.Context(), vm.SlotWork, gen, u) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(g.kills()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no kill sent")
		}
		time.Sleep(time.Millisecond)
	}
	_, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, u.ProjectID)
	requireSessionRule(t, err, kills.RuleKillPending)
	if r := tracker.CheckSave(vm.SlotWork, gen); r == nil || r.Rule != kills.RuleKillPending {
		t.Fatalf("save = %v", r)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("KillUser: %v", err)
	}
	if _, err := h.links.EnsureProjectUser(t.Context(), vm.SlotWork, gen, u.ProjectID); err != nil {
		t.Fatalf("session after a clean kill: %v", err)
	}
	if r := tracker.CheckSave(vm.SlotWork, gen); r != nil {
		t.Fatalf("save = %v", r)
	}

	// A canceled call isn't sent, and its kill fails closed.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := h.links.KillUser(ctx, vm.SlotWork, gen, u); !errors.Is(err, ErrKillNotSent) {
		t.Fatalf("KillUser with a canceled context = %v", err)
	}
	if r := tracker.CheckSave(vm.SlotWork, gen); r == nil || r.Rule != kills.RuleKillPending {
		t.Fatalf("save after a canceled kill = %v", r)
	}
	// After a cold stop of the generation, no kill begins in it.
	tracker.ColdStop(vm.SlotWork, gen, "test")
	sent := len(g.kills())
	if err := h.links.KillUser(t.Context(), vm.SlotWork, gen, u); !errors.Is(err, ErrKillNotSent) {
		t.Fatalf("KillUser after a cold stop = %v", err)
	}
	if n := len(g.kills()); n != sent {
		t.Fatalf("%d kills sent after a cold stop, want %d", n, sent)
	}

	if err := h.links.KillUser(t.Context(), vm.SlotWork, gen+1, u); !errors.Is(err, ErrKillNotSent) {
		t.Fatalf("KillUser in a generation that doesn't run = %v", err)
	}
	if r := tracker.CheckSave(vm.SlotWork, gen); r != nil {
		t.Fatalf("save after a kill of another generation = %v", r)
	}
}
