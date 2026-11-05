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

// accounting returns an accounting writer and the fake wb-hostd at the
// far end, which read the writer's version record.
func accounting(t *testing.T) (*hostlink.Accounting, *nt.Peer) {
	t.Helper()
	ours, theirs := nt.ChanPair(t)
	a, err := hostlink.NewAccounting(ours, nt.Own, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	hostd := nt.NewPeer(t, theirs)
	hostd.ReadVersion()
	return a, hostd
}

func writeWithin(a *hostlink.Accounting) error {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return a.Release(ctx, netip.MustParseAddr("198.18.0.1"))
}

// TestAccountingVersionRefused checks that a channel whose wb-hostd
// sends another build's version record ends, and every write fails.
func TestAccountingVersionRefused(t *testing.T) {
	a, hostd := accounting(t)
	if err := hostd.Ch.Send(hostversion.Record("9.9.9-dev+other"), nil); err != nil {
		t.Fatal(err)
	}
	if err := writeWithin(a); !errors.Is(err, hostlink.ErrChannelEnded) {
		t.Fatalf("write: %v, want ErrChannelEnded", err)
	}
	if got := hostd.Queued(); len(got) != 0 {
		t.Fatalf("wb-hostd got %d records", len(got))
	}
}

// TestAccountingDropsRecords checks that a record wb-hostd sends after
// its version record is logged and dropped, and writes go on.
func TestAccountingDropsRecords(t *testing.T) {
	a, hostd := accounting(t)
	hostd.SendVersion()
	if err := hostd.Ch.Send([]byte("unexpected"), nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for a.Recorder().Count(netd.RuleControlMessageRefused) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := a.Recorder().Count(netd.RuleControlMessageRefused); n != 1 {
		t.Fatalf("control-message-refused counted %d, want 1", n)
	}
	if err := writeWithin(a); err != nil {
		t.Fatal(err)
	}
	if got := hostd.Queued(); len(got) != 1 {
		t.Fatalf("wb-hostd got %d records, want 1", len(got))
	}
}
