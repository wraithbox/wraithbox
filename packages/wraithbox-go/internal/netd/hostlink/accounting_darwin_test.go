package hostlink_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/hostlink"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

// accounting returns an accounting writer whose wait for the version
// record is bounded by d, zero for hostversion.Timeout, the fake
// wb-hostd at the far end, which read the writer's version record, and
// a copy of wb-hostd's descriptor.
func accounting(t *testing.T, d time.Duration) (*hostlink.Accounting, *nt.Peer, rawEnd) {
	t.Helper()
	theirs, ours, raw := rawPair(t)
	a, err := hostlink.NewAccounting(hostlink.AccountingConfig{
		Conn: ours, Own: nt.Own, Log: slog.New(slog.DiscardHandler), VersionTimeout: d,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	hostd := nt.NewPeer(t, theirs)
	hostd.ReadVersion()
	return a, hostd, raw
}

func writeWithin(a *hostlink.Accounting) error {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return a.Release(ctx, netip.MustParseAddr("198.18.0.1"))
}

// TestAccountingVersionRefused checks that a channel whose wb-hostd
// sends another build's version record, or none within the version
// timeout, ends, and every write fails.
func TestAccountingVersionRefused(t *testing.T) {
	for name, send := range map[string]func(t *testing.T, p *nt.Peer){
		"another version": func(t *testing.T, p *nt.Peer) {
			if err := p.Ch.Send(hostversion.Record("9.9.9-dev+other"), nil); err != nil {
				t.Fatal(err)
			}
		},
		"no version record": func(*testing.T, *nt.Peer) { time.Sleep(100 * time.Millisecond) },
	} {
		t.Run(name, func(t *testing.T) {
			a, hostd, _ := accounting(t, 50*time.Millisecond)
			send(t, hostd)
			if err := writeWithin(a); !errors.Is(err, hostlink.ErrChannelEnded) {
				t.Fatalf("write: %v, want ErrChannelEnded", err)
			}
			if n := a.Recorder().Count(netd.RuleControlMessageRefused); n != 1 {
				t.Errorf("control-message-refused counted %d, want 1", n)
			}
			if got := hostd.Queued(); len(got) != 0 {
				t.Fatalf("wb-hostd got %d records", len(got))
			}
		})
	}
}

// TestAccountingEnds checks that a record wb-hostd sends after its
// version record, or one fdchan refuses, ends the channel, logged with
// control-message-refused, and later writes fail
// (S04-08-records-and-bounds, "Accounting and event channels").
func TestAccountingEnds(t *testing.T) {
	for name, send := range map[string]func(t *testing.T, p *nt.Peer, raw rawEnd){
		"a record": func(t *testing.T, p *nt.Peer, _ rawEnd) {
			if err := p.Ch.Send([]byte("unexpected"), nil); err != nil {
				t.Fatal(err)
			}
		},
		"two descriptors": func(t *testing.T, _ *nt.Peer, raw rawEnd) { sendTwo(t, raw, []byte("unexpected")) },
	} {
		t.Run(name, func(t *testing.T) {
			a, hostd, raw := accounting(t, 0)
			hostd.SendVersion()
			if err := writeWithin(a); err != nil {
				t.Fatal(err)
			}
			send(t, hostd, raw)
			deadline := time.Now().Add(time.Second)
			for a.Recorder().Count(netd.RuleControlMessageRefused) == 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if n := a.Recorder().Count(netd.RuleControlMessageRefused); n != 1 {
				t.Fatalf("control-message-refused counted %d, want 1", n)
			}
			if err := writeWithin(a); !errors.Is(err, hostlink.ErrChannelEnded) {
				t.Fatalf("write: %v, want ErrChannelEnded", err)
			}
			if got := hostd.Queued(); len(got) != 1 {
				t.Fatalf("wb-hostd got %d records, want the one before the end", len(got))
			}
		})
	}
}
