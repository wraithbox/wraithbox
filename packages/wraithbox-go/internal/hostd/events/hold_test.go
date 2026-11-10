package events

import (
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
)

// The holds of S04-08-records-and-bounds, "Host work the guest can
// cause": a sender that ignores its budget is held back, never dropped.

// overMargin is a budget the VM passed by the margin this minute.
func overMargin() audit.Budget {
	return audit.Budget{
		Rule: audit.RuleBudget, Until: time.Now().Add(time.Minute),
		EventsThisMinute: 100 + eventsMargin, EventsPerMinute: 100, BytesPerDay: 1 << 30,
	}
}

func (l *fakeLog) setFull(full chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.full = full
}

// stillQueued checks that conn's records stay unread for a while: the
// receiver holds its reads.
func stillQueued(t *testing.T, conn *fakeConn, want int) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	if n := len(conn.in); n != want {
		t.Fatalf("%d records left unread, want %d", n, want)
	}
}

// TestQueueFullHoldsReads: while the writer's queue is at its cap, the
// receiver reads nothing, logs the hold with its rule, and reads on once
// the queue has room. Nothing is dropped and the sender isn't ended.
func TestQueueFullHoldsReads(t *testing.T) {
	s := newSetup()
	full := make(chan struct{})
	s.log.setFull(full)
	conn, end := newFakeConn(), &ender{}
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	conn.push(marshal(t, eventRecord(2, work, validEvent())))
	r := s.hub.Attach(conn, Netd(work), end)
	stillQueued(t, conn, 2)
	if !strings.Contains(s.logText(), "rule="+RuleQueueFull) || !strings.Contains(s.logText(), "decision=hold") {
		t.Fatalf("log has no hold:\n%s", s.logText())
	}
	s.log.setFull(nil)
	close(full)
	close(conn.in)
	waitDone(t, r)
	if n := len(s.log.got()); n != 2 || len(end.got()) != 0 {
		t.Fatalf("%d records written, ends %v", n, end.got())
	}
}

// TestQueueFullReleasedWhenSenderGone: a queue that stays full, as while
// writes fail, doesn't keep a VM stop waiting: once the sender is gone
// the receiver reads what it left and stops.
func TestQueueFullReleasedWhenSenderGone(t *testing.T) {
	s := newSetup()
	s.log.setFull(make(chan struct{}))
	conn, end := newFakeConn(), &ender{}
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	r := s.hub.Attach(conn, Netd(work), end)
	stillQueued(t, conn, 1)
	close(conn.in)
	r.SenderGone()
	r.SenderGone()
	waitDone(t, r)
	if n := len(s.log.got()); n != 1 || len(end.got()) != 0 {
		t.Fatalf("%d records written, ends %v", n, end.got())
	}
	if !strings.Contains(s.logText(), "decision=drain") {
		t.Fatalf("log has no drain:\n%s", s.logText())
	}
}

// TestBudgetIgnoredHoldsReads: a record that leaves its VM past the
// budget by the margin holds the next read until the budget turns, with
// a log line of the rule. The record itself is written.
func TestBudgetIgnoredHoldsReads(t *testing.T) {
	s := newSetup()
	s.hub.recheck = time.Millisecond
	s.log.setBudget(overMargin())
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	waitFor(t, func() bool { return len(s.log.got()) == 1 })
	conn.push(marshal(t, eventRecord(2, work, validEvent())))
	stillQueued(t, conn, 1)
	if !strings.Contains(s.logText(), "rule="+RuleBudgetIgnored) {
		t.Fatalf("log has no hold:\n%s", s.logText())
	}
	// Events back under the margin and bytes past theirs: still held.
	b := overMargin()
	b.EventsThisMinute--
	b.BytesToday = b.BytesPerDay + bytesMargin
	s.log.setBudget(b)
	stillQueued(t, conn, 1)
	s.log.setBudget(audit.Budget{EventsPerMinute: 100, BytesPerDay: 1 << 30})
	close(conn.in)
	waitDone(t, r)
	if n := len(s.log.got()); n != 2 || len(end.got()) != 0 {
		t.Fatalf("%d records written, ends %v", n, end.got())
	}
}

// TestBudgetUnderMarginReads: a VM over its budget but within the
// margin is read on: a sender that heeds the notice still sends the
// records of work it admitted.
func TestBudgetUnderMarginReads(t *testing.T) {
	s := newSetup()
	b := overMargin()
	b.EventsThisMinute--
	s.log.setBudget(b)
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	conn.push(marshal(t, eventRecord(2, work, validEvent())))
	close(conn.in)
	waitDone(t, r)
	if n := len(s.log.got()); n != 2 || strings.Contains(s.logText(), RuleBudgetIgnored) {
		t.Fatalf("%d records written, log:\n%s", n, s.logText())
	}
}

// TestBudgetHoldReleasedWhenSenderGone: a VM stop doesn't wait for the
// budget's turn.
func TestBudgetHoldReleasedWhenSenderGone(t *testing.T) {
	s := newSetup()
	s.log.setBudget(overMargin())
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	conn.push(marshal(t, eventRecord(2, work, validEvent())))
	stillQueued(t, conn, 1)
	close(conn.in)
	r.SenderGone()
	waitDone(t, r)
	if n := len(s.log.got()); n != 2 || len(end.got()) != 0 {
		t.Fatalf("%d records written, ends %v", n, end.got())
	}
}

// TestProxydHoldEndsWithTheVM: wb-proxyd held for a VM reads on once
// the VM stops.
func TestProxydHoldEndsWithTheVM(t *testing.T) {
	s := newSetup()
	s.hub.recheck = time.Millisecond
	s.log.setBudget(overMargin())
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Proxyd(), end)
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	conn.push(marshal(t, eventRecord(2, isolated, validEvent())))
	stillQueued(t, conn, 1)
	s.hub.End(work)
	waitFor(t, func() bool { return len(s.log.got()) == 2 })
	s.log.setBudget(audit.Budget{EventsPerMinute: 100, BytesPerDay: 1 << 30})
	close(conn.in)
	waitDone(t, r)
	if len(end.got()) != 0 {
		t.Fatalf("ends %v", end.got())
	}
}
