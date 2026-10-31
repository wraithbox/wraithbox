package shells

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// fakeKiller records each kill, and returns err.
type fakeKiller struct {
	mu    sync.Mutex
	err   error
	kills []string
}

func (k *fakeKiller) KillUser(_ context.Context, slot vm.Slot, generation uint64, u state.ProjectUser) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.kills = append(k.kills, fmt.Sprintf("%s/%d/%s", slot, generation, u.ProjectID))
	return k.err
}

func (k *fakeKiller) got() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Sorted(slices.Values(k.kills))
}

func user(projectID string) state.ProjectUser {
	return state.ProjectUser{ProjectID: projectID, UID: 600, UserName: "wbp-" + projectID}
}

// open registers and binds a shell of projectID in the work VM's run 1.
func open(t *testing.T, g *Registry, id, projectID string) *Shell {
	t.Helper()
	sh, err := g.Register(id, vm.SlotWork, projectID, 42, true)
	if err != nil {
		t.Fatalf("Register(%s): %v", id, err)
	}
	sh.Bind(1, user(projectID))
	return sh
}

// TestRegisterCap sees the 65th shell of a VM refused with
// vm-session-cap, while the other VM still takes one.
func TestRegisterCap(t *testing.T) {
	g := NewRegistry(&fakeKiller{}, nil)
	defer g.Close()
	for i := range MaxPerVM {
		if _, err := g.Register(fmt.Sprintf("s%d", i), vm.SlotWork, "p", 1, false); err != nil {
			t.Fatalf("shell %d: %v", i, err)
		}
	}
	_, err := g.Register("one-more", vm.SlotWork, "p", 1, false)
	if r, ok := errors.AsType[*CapRefusal](err); !ok || r.Slot != vm.SlotWork {
		t.Fatalf("65th shell: %v", err)
	}
	if _, err := g.Register("other", vm.SlotIsolated, "p", 1, false); err != nil {
		t.Fatalf("shell in the other VM: %v", err)
	}
	if got := len(g.Sessions(vm.SlotWork)); got != MaxPerVM {
		t.Fatalf("Sessions = %d", got)
	}
}

func TestRegisterIDTaken(t *testing.T) {
	g := NewRegistry(&fakeKiller{}, nil)
	defer g.Close()
	open(t, g, "a", "p")
	if _, err := g.Register("a", vm.SlotWork, "p", 1, false); !errors.Is(err, ErrIDTaken) {
		t.Fatalf("Register of a taken id: %v", err)
	}
}

// TestSessions sees an open shell listed with its state and pid, and a
// released one gone.
func TestSessions(t *testing.T) {
	g := NewRegistry(&fakeKiller{}, nil)
	defer g.Close()
	sh := open(t, g, "a", "p")
	sh.SetState(vm.SessionRunning)
	got := g.Sessions(vm.SlotWork)
	if len(got) != 1 || got[0] != (vm.Session{ID: "a", State: vm.SessionRunning, PID: 42}) {
		t.Fatalf("Sessions = %+v", got)
	}
	sh.Release()
	if got := g.Sessions(vm.SlotWork); len(got) != 0 {
		t.Fatalf("Sessions after Release = %+v", got)
	}
}

// TestEndLastKills sees the last shell of a project kill its user, and
// one with another shell of the project open leave it.
func TestEndLastKills(t *testing.T) {
	var logs bytes.Buffer
	k := &fakeKiller{}
	g := NewRegistry(k, slog.New(slog.NewJSONHandler(&logs, nil)))
	a, b := open(t, g, "a", "p"), open(t, g, "b", "p")
	a.End(t.Context(), false)
	g.kills.Wait()
	if got := k.got(); len(got) != 0 {
		t.Fatalf("kills after a non-last shell = %v", got)
	}
	if !strings.Contains(logs.String(), `"decision":"skip"`) || !strings.Contains(logs.String(), `"rule":"shell-user-kill"`) {
		t.Fatalf("log = %s", logs.String())
	}
	b.End(t.Context(), false)
	g.Close()
	if got := k.got(); !slices.Equal(got, []string{"work/1/p"}) {
		t.Fatalf("kills after the last shell = %v", got)
	}
}

// TestKillingWaits sees a new shell of the project wait while its kill
// runs, and not after.
func TestKillingWaits(t *testing.T) {
	release := make(chan struct{})
	k := &blockingKiller{release: release}
	g := NewRegistry(k, nil)
	open(t, g, "a", "p").End(t.Context(), false)
	if !g.Killing(vm.SlotWork, 1, "p") {
		t.Fatal("Killing = false while the kill runs")
	}
	if g.Killing(vm.SlotWork, 1, "q") {
		t.Fatal("Killing = true for another project")
	}
	close(release)
	g.Close()
	if g.Killing(vm.SlotWork, 1, "p") {
		t.Fatal("Killing = true after the kill")
	}
}

type blockingKiller struct{ release chan struct{} }

func (k *blockingKiller) KillUser(context.Context, vm.Slot, uint64, state.ProjectUser) error {
	<-k.release
	return nil
}

// TestLostKillsOnNextLink sees a lost shell's user killed on the next
// connection, a user with an open shell left alone, and a user without
// a lost shell not killed.
func TestLostKillsOnNextLink(t *testing.T) {
	k := &fakeKiller{}
	g := NewRegistry(k, nil)
	open(t, g, "a", "lost").End(t.Context(), true)
	open(t, g, "b", "busy").End(t.Context(), true)
	open(t, g, "c", "busy")
	open(t, g, "d", "other")
	if got := k.got(); len(got) != 0 {
		t.Fatalf("kills before the next link = %v", got)
	}
	g.OnLinked(vm.SlotWork, 1)
	g.Close()
	if got := k.got(); !slices.Equal(got, []string{"work/1/lost"}) {
		t.Fatalf("kills on the next link = %v", got)
	}
}

// TestLostKillRetried sees a kill that didn't reach the guest sent again
// on the next connection.
func TestLostKillRetried(t *testing.T) {
	k := &fakeKiller{err: fmt.Errorf("link gone: %w", guestconn.ErrKillNotSent)}
	g := NewRegistry(k, nil)
	open(t, g, "a", "p").End(t.Context(), false)
	g.kills.Wait()
	k.mu.Lock()
	k.err = nil
	k.mu.Unlock()
	g.OnLinked(vm.SlotWork, 1)
	g.Close()
	if got := k.got(); !slices.Equal(got, []string{"work/1/p", "work/1/p"}) {
		t.Fatalf("kills = %v", got)
	}
}

// TestNewRunDropsLost sees the lost users of an older run dropped: they
// went with its VM.
func TestNewRunDropsLost(t *testing.T) {
	k := &fakeKiller{}
	g := NewRegistry(k, nil)
	open(t, g, "a", "p").End(t.Context(), true)
	g.OnLinked(vm.SlotWork, 2)
	g.Close()
	if got := k.got(); len(got) != 0 {
		t.Fatalf("kills = %v", got)
	}
}
