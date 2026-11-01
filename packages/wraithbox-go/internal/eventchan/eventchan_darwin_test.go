package eventchan

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// realPair returns the two ends of an fdchan socketpair: the sender's
// and wb-hostd's.
func realPair(t *testing.T) (sender, hostd *fdchan.Channel) {
	t.Helper()
	fa, fb, err := fdchan.Socketpair(MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	sender, err = fdchan.New(fa, MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	hostd, err = fdchan.New(fb, MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostd.Close() })
	return sender, hostd
}

// TestRealChannelFullPeerBlocks: on a real socketpair whose reader
// doesn't read, a write fails with ENOBUFS or EAGAIN, Admit refuses with
// event-channel-blocked, and once wb-hostd reads every record arrives
// once, in order (S04-08-records-and-bounds, "Event records").
func TestRealChannelFullPeerBlocks(t *testing.T) {
	sender, hostd := realPair(t)
	h := start(t, Config{Conn: sender})
	n := 0
	for ; h.s.Admit(work, net4001) == ""; n++ {
		if n > 10_000 {
			t.Fatal("the channel never blocked")
		}
		e := event("r")
		e.HttpPath = strings.Repeat("p", MaxPath)
		h.s.Emit(work, e)
		time.Sleep(100 * time.Microsecond)
	}
	if !strings.Contains(h.log.String(), "a write failed") {
		t.Fatalf("log lacks the failed write:\n%s", h.log)
	}
	_ = hostd.SetReadDeadline(time.Now().Add(10 * time.Second))
	for want := uint64(1); want <= uint64(n); want++ {
		m, err := hostd.Recv()
		if err != nil {
			t.Fatalf("record %d: %v", want, err)
		}
		r := &auditv1.EventRecord{}
		if err := proto.Unmarshal(m.Record, r); err != nil {
			t.Fatalf("record %d: %v", want, err)
		}
		if r.GetSequence() != want {
			t.Fatalf("sequence %d, want %d", r.GetSequence(), want)
		}
	}
	eventually(t, "the unblock", func() bool { return h.s.Admit(work, net4001) == "" })
}

// TestRealChannelNotice: a notice wb-hostd sends on the real channel
// refuses new work of its VM.
func TestRealChannelNotice(t *testing.T) {
	sender, hostd := realPair(t)
	h := start(t, Config{Conn: sender})
	if err := hostd.Send(noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, t0.Add(time.Hour)), nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the notice", func() bool { return h.s.Admit(work, net4001) == RuleBytes })
}
