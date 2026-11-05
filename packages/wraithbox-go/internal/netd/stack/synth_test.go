package stack

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/synth"
)

// TestSyntheticPool checks the synthetic pool as the stack's stream
// policy (S07-02-packet-path, "Connections"): TCP to a mapped address on
// an allowed port reaches the hand-off with its name, and TCP to an
// unmapped address or a disallowed port is reset at the first SYN with
// its rule.
func TestSyntheticPool(t *testing.T) {
	calls := &nt.Calls{}
	ports := &nt.Ports{}
	handOff := make(nt.HandOff, 1)
	pool, err := synth.New(netd.DefaultSynthetic, nil, synth.Deps{
		Accounting: nt.Accounting{Calls: calls},
		Reports:    nt.NameReports{Calls: calls},
		Ports:      ports,
		HandOff:    handOff,
		Log:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	name := nt.Name("api.example.com")
	ports.Allow(name, 443)
	mapped, rule := pool.Answer(context.Background(), name)
	if rule != "" {
		t.Fatal(rule)
	}
	g := newGuest(t)
	h := g.attach(t, nt.Config(), netd.Hooks{Stream: pool})

	c, err := g.dial(netip.AddrPortFrom(mapped, 443))
	if err != nil {
		t.Fatal(err)
	}
	var s nt.Stream
	select {
	case s = <-handOff:
	case <-time.After(5 * time.Second):
		t.Fatal("no stream reached the hand-off")
	}
	if s.Flow.Name != name || s.Flow.Dest != netip.AddrPortFrom(mapped, 443) || s.Flow.Guest.Addr() != nt.Leased {
		t.Fatalf("handed %+v", s.Flow)
	}
	roundTrip(t, c, s.Conn)
	_ = c.Close()
	_ = s.Conn.Close()

	unmapped := mapped.Next()
	for _, tc := range []struct {
		dest netip.AddrPort
		rule netd.Rule
	}{
		{netip.AddrPortFrom(unmapped, 443), netd.RuleSyntheticUnmapped},
		{netip.AddrPortFrom(mapped, 22), netd.RulePortNotAllowed},
	} {
		if _, err := g.dial(tc.dest); !isRefused(err) {
			t.Fatalf("%v: dial = %v, want refused", tc.dest, err)
		}
		if n := g.synCount(tc.dest); n != 1 {
			t.Errorf("%v: the guest sent %d SYNs, want 1", tc.dest, n)
		}
		if n := h.s.Recorder().Count(tc.rule); n != 1 {
			t.Errorf("%v: %s count %d, want 1", tc.dest, tc.rule, n)
		}
	}
}
