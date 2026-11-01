//go:build unix

package guestconn

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/kills"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

func (g *usersGuest) UnlockUser(_ context.Context, req *guestv1.UnlockUserRequest) (*guestv1.UnlockUserResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.unlockErr != nil {
		return nil, g.unlockErr
	}
	g.unlocked = append(g.unlocked, req.GetUid())
	return &guestv1.UnlockUserResponse{}, nil
}

// Shell answers each shell with exit 7.
func (*usersGuest) Shell(stream guestv1.GuestService_ShellServer) error {
	return stream.Send(&guestv1.ShellResponse{Frame: &terminalv1.ServerFrame{Frame: &terminalv1.ServerFrame_Exit{
		Exit: &terminalv1.Exit{Status: &terminalv1.Exit_Code{Code: 7}},
	}}})
}

func (g *usersGuest) unlocks() []uint32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.unlocked)
}

// shellHarness is a linked harness whose guest is g, with one project
// registered, whose OnLinked hook sends each generation on linked. It
// returns the generation once a session can start in it.
func shellHarness(t *testing.T, g *usersGuest, linked chan uint64) (*harness, uint64) {
	t.Helper()
	h := newHarness(t, own, own, true)
	h.links.Close()
	h.cfg.Kills = kills.New(kills.Config{Logger: slog.New(slog.NewJSONHandler(h.logs, nil)), Audit: &audittest.Recorder{}})
	h.cfg.OnLinked = func(_ vm.Slot, generation uint64) { linked <- generation }
	ls, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.links = ls
	t.Cleanup(ls.Close)
	h.users = g
	h.link()
	h.register(1)
	gen := h.links.Status(vm.SlotWork).Generation
	h.waitAdmitted(gen)
	return h, gen
}

// TestOnLinked sees the hook called with the generation of each new
// link.
func TestOnLinked(t *testing.T) {
	linked := make(chan uint64, 8)
	_, gen := shellHarness(t, &usersGuest{}, linked)
	select {
	case got := <-linked:
		if got != gen {
			t.Fatalf("OnLinked generation %d, want %d", got, gen)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnLinked wasn't called")
	}
}

// TestUnlockUser sees an unlock reach the guest, a guest refusal fail
// closed with session-unlock-failed, and an unlock in a generation
// without a link refused.
func TestUnlockUser(t *testing.T) {
	g := &usersGuest{}
	h, gen := shellHarness(t, g, make(chan uint64, 8))
	u := usersOf(t, h, gen, 1)[0]
	if err := h.links.UnlockUser(t.Context(), vm.SlotWork, gen, u); err != nil {
		t.Fatal(err)
	}
	if got := g.unlocks(); !slices.Equal(got, []uint32{u.UID}) {
		t.Fatalf("unlocked = %v", got)
	}
	g.mu.Lock()
	g.unlockErr = status.Error(codes.FailedPrecondition, "no such user")
	g.mu.Unlock()
	requireSessionRule(t, h.links.UnlockUser(t.Context(), vm.SlotWork, gen, u), RuleUnlockFailed)
	requireSessionRule(t, h.links.UnlockUser(t.Context(), vm.SlotWork, gen+1, u), RuleSessionNoLink)
}

// TestOpenShell sees a shell stream opened on the link, with the link's
// down channel, and one in a generation without a link refused.
func TestOpenShell(t *testing.T) {
	h, gen := shellHarness(t, &usersGuest{}, make(chan uint64, 8))
	stream, down, err := h.links.OpenShell(t.Context(), vm.SlotWork, gen)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := stream.Recv()
	if err != nil || resp.GetFrame().GetExit().GetCode() != 7 {
		t.Fatalf("Recv = %v, %v", resp, err)
	}
	select {
	case <-down:
		t.Fatal("down is closed on a live link")
	default:
	}
	_, _, err = h.links.OpenShell(t.Context(), vm.SlotWork, gen+1)
	requireSessionRule(t, err, RuleSessionNoLink)
}
