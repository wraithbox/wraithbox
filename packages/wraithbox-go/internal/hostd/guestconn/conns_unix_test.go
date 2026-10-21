//go:build unix

package guestconn

import (
	"errors"
	"net"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
)

// requireRefusal checks that err is a *Refusal with rule, logged with
// that rule.
func requireRefusal(t *testing.T, h *harness, err error, rule string) {
	t.Helper()
	r, ok := errors.AsType[*Refusal](err)
	if !ok || r.Rule != rule {
		t.Fatalf("err = %v, want a refusal with %s", err, rule)
	}
	if len(h.logs.withRule(t, rule)) == 0 {
		t.Fatalf("no log record with %s", rule)
	}
}

// openWork opens a connection to wb-guestd's port in generation 1 of
// the work VM.
func (h *harness) openWork(project string) (net.Conn, error) {
	return h.links.open(h.t.Context(), vm.SlotWork, 1, guestsock.Port, project)
}

// Every routed port is below 1024 (S04-02-vm-provider, "Guest ports
// below 1024"), and wb-guestd's is routed.
func TestRoutesBelow1024(t *testing.T) {
	for port, r := range routes {
		if port == 0 || port >= 1024 {
			t.Errorf("route %d (%s) isn't a port from 1 to 1023", port, r.name)
		}
	}
	if _, ok := routes[guestsock.Port]; !ok {
		t.Error("wb-guestd's port isn't routed")
	}
}

// Port 1024 and ports not in the table are refused with
// guest-port-unrouted before the VM manager or the provider is asked.
func TestC11GuestLinkPortUnrouted(t *testing.T) {
	h := newHarness(t, own, own, false)
	h.start()
	for _, port := range []uint32{0, 22, 1022, 1024, 65535} {
		_, err := h.links.open(t.Context(), vm.SlotWork, 1, port, "")
		requireRefusal(t, h, err, RulePortUnrouted)
	}
	if got := h.provider.Last().Connects(); len(got) != 0 {
		t.Fatalf("the provider was asked for %v", got)
	}
}

// At MaxConnsPerVM live connections the next is refused with
// guest-conn-cap-vm, and closing one makes room.
func TestC11GuestLinkCapVM(t *testing.T) {
	h := newHarness(t, own, own, false)
	h.start()
	conns := make([]net.Conn, 0, MaxConnsPerVM)
	for i := range MaxConnsPerVM {
		c, err := h.openWork("")
		if err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
		conns = append(conns, c)
	}
	_, err := h.openWork("")
	requireRefusal(t, h, err, RuleConnCapVM)
	if n := len(h.provider.Last().Connects()); n != MaxConnsPerVM {
		t.Fatalf("the provider was asked %d times, want %d", n, MaxConnsPerVM)
	}
	_ = conns[0].Close()
	_ = conns[0].Close()
	if _, err := h.openWork(""); err != nil {
		t.Fatalf("after a close: %v", err)
	}
	if _, err := h.openWork(""); err == nil {
		t.Fatal("a second Close gave back two places")
	}
}

// At MaxConnsPerProject live connections of a project the next is
// refused with guest-conn-cap-project, and another project isn't.
func TestC11GuestLinkCapProject(t *testing.T) {
	h := newHarness(t, own, own, false)
	h.start()
	for i := range MaxConnsPerProject {
		if _, err := h.openWork("p"); err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
	}
	_, err := h.openWork("p")
	requireRefusal(t, h, err, RuleConnCapProject)
	if _, err := h.openWork("q"); err != nil {
		t.Fatalf("another project: %v", err)
	}
}

// Past MaxOpensPerWindow opens in OpenWindow, an open is refused with
// guest-reconnect-rate, whether or not the others are still open. Once
// the window moves on, an open passes.
func TestC11GuestLinkOpenRate(t *testing.T) {
	h := newHarness(t, own, own, false)
	h.start()
	for i := range MaxOpensPerWindow {
		c, err := h.openWork("")
		if err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
		_ = c.Close()
	}
	_, err := h.openWork("")
	requireRefusal(t, h, err, RuleReconnectRate)
	h.clock.advance(OpenWindow)
	if _, err := h.openWork(""); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// A connection of a run that ended while it opened is closed at once,
// and gives its place back.
func TestC11GuestLinkStaleGeneration(t *testing.T) {
	h := newHarness(t, own, own, false)
	vc := h.links.vm(vm.SlotWork)
	if r := vc.reserve(""); r != nil {
		t.Fatal(r)
	}
	vc.closeGeneration(1)
	host, guest := net.Pipe()
	defer guest.Close()
	if _, ok := vc.track(host, 1, ""); ok {
		t.Fatal("a connection of an ended run was kept")
	}
	if _, err := host.Write([]byte{0}); err == nil {
		t.Fatal("the stale connection is still open")
	}
	if vc.liveCount() != 0 || vc.reserved != 0 {
		t.Fatalf("live %d, reserved %d after a stale connection", vc.liveCount(), vc.reserved)
	}
}
