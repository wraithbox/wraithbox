package events

import (
	"os"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// realPair returns the raw descriptor of a sender's end, for sends that
// bypass fdchan, and a receiver on wb-hostd's end.
func realPair(t *testing.T, s *setup, end *ender) (raw *os.File, r *Receiver) {
	t.Helper()
	fa, fb, err := fdchan.Socketpair(eventchan.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fa.Close() })
	ch, err := fdchan.New(fb, eventchan.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	return fa, s.hub.Attach(ch, Netd(work), end)
}

// TestRealChannelOversizeEndsSender: a record over 4 KiB, which the
// receiver reads truncated (MSG_TRUNC), ends the sender with fdchan's
// rule.
func TestRealChannelOversizeEndsSender(t *testing.T) {
	s, end := newSetup(), &ender{}
	raw, r := realPair(t, s, end)
	if err := syscall.Sendmsg(int(raw.Fd()), make([]byte, eventchan.MaxRecord+1), nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	waitDone(t, r)
	if got := end.got(); len(got) != 1 || got[0] != string(fdchan.RuleRecordSize) {
		t.Fatalf("ends %v, want [%s]", got, fdchan.RuleRecordSize)
	}
}

// TestRealChannelDescriptorEndsSender: a record with a descriptor ends
// the sender with event-record-invalid.
func TestRealChannelDescriptorEndsSender(t *testing.T) {
	s, end := newSetup(), &ender{}
	raw, r := realPair(t, s, end)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })
	rec := marshal(t, eventRecord(1, work, validEvent()))
	if err := syscall.Sendmsg(int(raw.Fd()), rec, syscall.UnixRights(int(pw.Fd())), nil, 0); err != nil {
		t.Fatal(err)
	}
	waitDone(t, r)
	if got := end.got(); len(got) != 1 || got[0] != RuleRecordInvalid || len(s.log.got()) != 0 {
		t.Fatalf("ends %v, records %d", got, len(s.log.got()))
	}
}

// TestRealChannelSenderExit: a sender that closes its end stops the
// receiver, with no end.
func TestRealChannelSenderExit(t *testing.T) {
	s, end := newSetup(), &ender{}
	raw, r := realPair(t, s, end)
	if err := syscall.Sendmsg(int(raw.Fd()), marshal(t, eventRecord(1, work, validEvent())), nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	waitDone(t, r)
	if len(end.got()) != 0 || len(s.log.got()) != 1 {
		t.Fatalf("ends %v, records %d", end.got(), len(s.log.got()))
	}
}
