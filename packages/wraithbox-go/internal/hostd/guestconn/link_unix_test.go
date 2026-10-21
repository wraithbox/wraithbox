//go:build unix

package guestconn

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestsock"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// link starts the work VM and waits until its control link is up.
func (h *harness) link() {
	h.t.Helper()
	h.start()
	if d := h.clock.nextWait(h.t); d != FirstWait {
		h.t.Fatalf("first wait %v, want %v", d, FirstWait)
	}
	h.clock.advance(FirstWait)
	if g := h.nextTime(); g != 1 {
		h.t.Fatalf("time hook for generation %d, want 1", g)
	}
	h.waitState(StateLinked)
}

// noRedial fails the test when grpc-go asked a link's dialer for a second
// connection: every connection must come through the caps and waits.
func (h *harness) noRedial() {
	h.t.Helper()
	if recs := h.logs.withRule(h.t, guestlink.RuleRedial); len(recs) != 0 {
		h.t.Fatalf("grpc-go redialed: %v", recs)
	}
}

// requireClosed checks that the host end of a connection is closed: its
// descriptor released, not only the gRPC client on it.
func requireClosed(t *testing.T, host net.Conn) {
	t.Helper()
	if _, err := host.Write([]byte{0}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write on the host end: %v, want net.ErrClosed", err)
	}
}

// A good hello links, on wb-guestd's port of the current generation, and
// the time hook is called after it.
func TestC11GuestLinkAccepts(t *testing.T) {
	h := newHarness(t, own, own, true)
	h.link()
	s := h.links.Status(vm.SlotWork)
	if s.Generation != 1 || s.Refusal != nil {
		t.Fatalf("status %+v", s)
	}
	if got := h.provider.Last().Connects(); len(got) != 1 || got[0] != (vmtest.Connect{Generation: 1, Port: guestsock.Port}) {
		t.Fatalf("connects %v, want one to generation 1, port %d", got, guestsock.Port)
	}
	h.noRedial()
}

// Another version, one outside the form, and an unstamped host are each
// refused with their rule before any other request: the time hook never
// runs. The status keeps both versions for `wb vm status`, and the log
// shows the guest's escaped and cut (S04-08-records-and-bounds, "With
// `wb-guestd`", "The guest's version is a label").
func TestC11GuestLinkVersionRefused(t *testing.T) {
	evil := "1.2.3\x1b]0;x\x07" + strings.Repeat("a", 100)
	tests := []struct {
		name, host, guest, rule string
	}{
		{"other version", own, "1.2.4", version.RuleMismatch},
		{"outside the form", own, evil, version.RuleForm},
		{"unstamped host and guest", "", "", version.RuleUnstamped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.host, tt.guest, true)
			h.start()
			h.clock.nextWait(t)
			h.clock.advance(FirstWait)
			requireVersionRefusal(t, h.waitState(StateRefused).Refusal, tt.host, tt.guest, tt.rule)
			requireVersionLogged(t, h, tt.guest, tt.rule)
			select {
			case g := <-h.times:
				t.Fatalf("time hook called for generation %d after a refusal", g)
			default:
			}
			// A refused hello is a failed try: the next wait doubles.
			if d := h.clock.nextWait(t); d != 2*FirstWait {
				t.Fatalf("wait after a refusal %v, want %v", d, 2*FirstWait)
			}
		})
	}
}

// requireVersionRefusal: the status names the rule, both versions as
// labels, and the fix (NFR06-explained-refusals).
func requireVersionRefusal(t *testing.T, r *HelloRefusal, host, guest, rule string) {
	t.Helper()
	if r == nil || r.Rule != rule || r.OwnVersion != version.Label(host) || r.GuestVersion != version.Label(guest) {
		t.Fatalf("refusal %+v, want %s with both versions", r, rule)
	}
	if !strings.Contains(r.Message, "Stop and start the VM") || !strings.Contains(r.Message, r.GuestVersion) {
		t.Errorf("message %q doesn't name the version and the fix", r.Message)
	}
}

// requireVersionLogged: one record with the rule, and the guest's
// version in it escaped and cut to 64 bytes.
func requireVersionLogged(t *testing.T, h *harness, guest, rule string) {
	t.Helper()
	recs := h.logs.withRule(t, rule)
	if len(recs) != 1 {
		t.Fatalf("%d records with %s, want 1", len(recs), rule)
	}
	gv, _ := recs[0]["guest_version"].(string)
	if gv != version.Label(guest) || strings.ContainsAny(gv, "\x1b\x07") || strings.Count(gv, "a") > version.MaxLen {
		t.Errorf("logged guest version %q, want it escaped and cut", gv)
	}
}

// The waits are 0.1, 0.2, 0.4 s and so on up to 5 s while every try
// fails, and no more than 20 connections are opened in a minute
// (S06-11-reconnect-and-recovery, "Reconnect").
func TestC11GuestLinkBackoff(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
	}{
		{"connection fails", func(h *harness) {
			h.provider.OnConnect = func(context.Context, *vmtest.Instance, uint64, uint32) (net.Conn, error) {
				return nil, &vm.ProviderRefusal{Rule: "vm-connect-failed", Detail: "no listener"}
			}
		}},
		{"hello refused", func(h *harness) { h.guestVersion = "9.9.9" }},
	}
	want := []time.Duration{
		100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond,
		1600 * time.Millisecond, 3200 * time.Millisecond, 5 * time.Second, 5 * time.Second, 5 * time.Second,
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, own, own, true)
			tt.setup(h)
			h.start()
			for i, w := range want {
				d := h.clock.nextWait(t)
				if d != w {
					t.Fatalf("wait %d = %v, want %v", i, d, w)
				}
				h.clock.advance(d)
				waitConnects(t, h, i+1)
			}
		})
	}
}

func waitConnects(t *testing.T, h *harness, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.provider.Last().Connects()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d connects, want %d", len(h.provider.Last().Connects()), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// A guest that drops the link right after each hello gets a new
// connection after 0.1 s each time, until 20 were opened in a minute.
// Then the link waits for the window, logs guest-reconnect-rate once,
// and never opens a 21st connection within a minute.
func TestC11GuestLinkReconnectRate(t *testing.T) {
	h := newHarness(t, own, own, true)
	h.start()
	for i := range MaxOpensPerWindow {
		if d := h.clock.nextWait(t); d != FirstWait {
			t.Fatalf("wait before connection %d = %v, want %v", i+1, d, FirstWait)
		}
		h.clock.advance(FirstWait)
		guest := h.nextGuest()
		h.nextTime()
		if d := h.clock.nextWait(t); d != TimeInterval {
			t.Fatalf("wait while linked = %v, want %v", d, TimeInterval)
		}
		_ = guest.Close()
	}
	// 20 opens at 0.1 s to 2.0 s: the next has room at 60.1 s.
	want := OpenWindow + FirstWait - MaxOpensPerWindow*FirstWait
	if d := h.clock.nextWait(t); d != want {
		t.Fatalf("wait past 20 opens = %v, want %v", d, want)
	}
	if n := len(h.provider.Last().Connects()); n != MaxOpensPerWindow {
		t.Fatalf("%d connections, want %d", n, MaxOpensPerWindow)
	}
	if recs := h.logs.withRule(t, RuleReconnectRate); len(recs) != 1 {
		t.Fatalf("%d %s records, want 1", len(recs), RuleReconnectRate)
	}
	h.clock.advance(want)
	h.nextGuest()
	h.nextTime()
	h.waitState(StateLinked)
	h.noRedial()
}

// Closing the guest end drops the link, and the link connects again
// after 0.1 s, without grpc-go redialing on its own.
func TestC11GuestLinkReconnects(t *testing.T) {
	h := newHarness(t, own, own, true)
	h.link()
	first, firstHost := h.nextGuest(), h.nextHost()
	if d := h.clock.nextWait(t); d != TimeInterval {
		t.Fatalf("wait while linked = %v", d)
	}
	_ = first.Close()
	if d := h.clock.nextWait(t); d != FirstWait {
		t.Fatalf("wait after a drop = %v, want %v", d, FirstWait)
	}
	h.clock.advance(FirstWait)
	h.nextGuest()
	h.nextTime()
	h.waitState(StateLinked)
	if recs := h.logs.withRule(t, RuleLinkDropped); len(recs) != 1 {
		t.Fatalf("%d drop records, want 1", len(recs))
	}
	if n := h.links.vm(vm.SlotWork).liveCount(); n != 1 {
		t.Fatalf("%d live connections, want the new one only", n)
	}
	requireClosed(t, firstHost)
	h.noRedial()
}

// The time hook runs after the hello, then every 60 s while the link is
// up (S06-06-devices-and-warm-start, "When the host sends the time").
func TestC11GuestLinkTimeSchedule(t *testing.T) {
	h := newHarness(t, own, own, true)
	h.link()
	for range 3 {
		if d := h.clock.nextWait(t); d != TimeInterval {
			t.Fatalf("wait = %v, want %v", d, TimeInterval)
		}
		h.clock.advance(TimeInterval)
		if g := h.nextTime(); g != 1 {
			t.Fatalf("time hook for generation %d", g)
		}
	}
}

// A VM stop ends the loop and closes every connection. The next run is a
// new generation with a new link, and the old generation's connection
// stays closed.
func TestC11GuestLinkStop(t *testing.T) {
	h := newHarness(t, own, own, true)
	h.link()
	old := h.nextHost()
	if err := h.manager.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	h.waitState(StateDown)
	requireClosed(t, old)
	if n := h.links.vm(vm.SlotWork).liveCount(); n != 0 {
		t.Fatalf("%d live connections after the stop", n)
	}
	h.start()
	// The old link's 60 s wait comes first.
	h.clock.skipTo(t, FirstWait)
	if g := h.nextTime(); g != 2 {
		t.Fatalf("time hook for generation %d, want 2", g)
	}
	s := h.waitState(StateLinked)
	if s.Generation != 2 {
		t.Fatalf("linked to generation %d", s.Generation)
	}
	got := h.provider.Last().Connects()
	if len(got) != 1 || got[0].Generation != 2 {
		t.Fatalf("connects of the new run %v", got)
	}
}

// Close ends every link and leaves no goroutine of the link, the gRPC
// client or the reconnect loop behind, after drops included.
func TestC11GuestLinkNoLeaks(t *testing.T) {
	h := newHarness(t, own, own, true)
	h.link()
	_ = h.nextGuest().Close()
	h.clock.skipTo(t, FirstWait)
	h.nextGuest()
	h.nextTime()
	h.waitState(StateLinked)
	if err := h.manager.Stop(t.Context(), vm.SlotWork); err != nil {
		t.Fatal(err)
	}
	h.waitState(StateDown)
	h.links.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		left := hostGoroutines()
		if left == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines left:\n%s", left)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// hostGoroutines returns the stacks of the goroutines of the link, of
// its loop, and of a gRPC client transport.
func hostGoroutines() string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(g, "guestconn.(*run)") || strings.Contains(g, "guestconn.(*Links).Run") ||
			strings.Contains(g, "guestlink.(*Link).watch") || strings.Contains(g, "transport.(*http2Client)") {
			out = append(out, g)
		}
	}
	return strings.Join(out, "\n\n")
}
