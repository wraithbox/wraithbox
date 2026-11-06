package egress

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// startReader starts a reader of wb-netd's channel for the work VM and
// checks that it sent wb-hostd's version record.
func startReader(t *testing.T, s *State, src Source) (*fakeConn, *ender, *Reader) {
	t.Helper()
	conn, end := newFakeConn(), newEnder()
	r := s.Read(conn, src, end, own)
	conn.expectVersion(t)
	return conn, end, r
}

func done(t *testing.T, r *Reader) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(wait):
		t.Fatal("the reader didn't stop")
	}
}

// TestReaderKeepsRecords: the reader applies each record after the
// version record, and reads on until the sender closes the channel,
// which ends nothing.
func TestReaderKeepsRecords(t *testing.T) {
	s, _, buf := newState(t)
	s.Start("work", false)
	conn, end, r := startReader(t, s, NetdSource("work"))
	conn.push(hostversion.Record(own))
	conn.push(entryRecord(t, "a.example.com", wire(1)))
	conn.push(debitRecord(t, "a.example.com"))
	close(conn.in)
	done(t, r)
	if got := end.got(); len(got) != 0 {
		t.Fatalf("ended with %q", got)
	}
	if entries, debited, _ := s.Kept("work"); len(entries) != 1 || len(debited) != 1 {
		t.Fatalf("kept %v and %v", entries, names(debited))
	}
	expectLog(t, buf, "accounting channel closed by its sender", `"records":2`)
	if !conn.isClosed() {
		t.Fatal("the reader didn't close the channel")
	}
}

// TestRefusedRecordEndsSender: a refused record is logged with its rule
// and check, ends the sender once, and nothing after it is read.
func TestRefusedRecordEndsSender(t *testing.T) {
	tests := []struct {
		name  string
		bad   func(t *testing.T) []byte
		rule  string
		check string
	}{
		{"entry", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "A.example.com", wire(2)) }, RuleEntryInvalid, "name-form"},
		{"conflict", func(t *testing.T) []byte { t.Helper(); return entryRecord(t, "b.example.com", wire(1)) }, RuleEntryInvalid, CheckMappingConflict},
		{"release", func(t *testing.T) []byte { t.Helper(); return releaseRecord(t, 1) }, RuleRecordMalformed, "synthetic-range"},
		{"garbage", func(*testing.T) []byte { return []byte("not a record") }, RuleRecordMalformed, "message-form"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, buf := newState(t)
			s.Start("work", false)
			conn, end, r := startReader(t, s, NetdSource("work"))
			conn.push(hostversion.Record(own))
			conn.push(entryRecord(t, "a.example.com", wire(1)))
			conn.push(tt.bad(t))
			conn.push(entryRecord(t, "c.example.com", wire(3)))
			done(t, r)
			end.expect(t, tt.rule)
			expectLog(t, buf, `"rule":"`+tt.rule+`"`, `"decision":"end-sender"`, `"check":"`+tt.check+`"`,
				`"sender":"wb-netd"`, `"vm":"work"`, `"after_records":1`)
			if entries, _, _ := s.Kept("work"); len(entries) != 1 {
				t.Fatalf("kept %v, want only the record before the refusal", entries)
			}
		})
	}
}

// TestReaderVersionRefused: no version record within the timeout,
// another build's, or one with a descriptor ends the sender before any
// record is read.
func TestReaderVersionRefused(t *testing.T) {
	tests := []struct {
		name string
		push func(t *testing.T, c *fakeConn)
		rule string
	}{
		{"timeout", func(_ *testing.T, c *fakeConn) { c.pushErr(fmt.Errorf("read: %w", os.ErrDeadlineExceeded)) }, hostversion.RuleTimeout},
		{"mismatch", func(_ *testing.T, c *fakeConn) { c.push(hostversion.Record("9.9.9")) }, hostversion.RuleMismatch},
		{"a record first", func(_ *testing.T, c *fakeConn) { c.push([]byte("WBV1")) }, hostversion.RuleMalformed},
		{"descriptor", func(t *testing.T, c *fakeConn) {
			t.Helper()
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			c.in <- received{m: &fdchan.Message{Record: hostversion.Record(own), File: r}}
		}, RuleRecordMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, buf := newState(t)
			s.Start("work", false)
			conn, end, r := startReader(t, s, NetdSource("work"))
			tt.push(t, conn)
			conn.push(entryRecord(t, "a.example.com", wire(1)))
			done(t, r)
			end.expect(t, tt.rule)
			expectLog(t, buf, `"rule":"`+tt.rule+`"`, `"decision":"end-sender"`)
			if entries, _, _ := s.Kept("work"); len(entries) != 0 {
				t.Fatalf("kept %v", entries)
			}
		})
	}
}

// TestRecvErrorsEndSender: a datagram the channel refuses, such as one
// over 4 KiB or received with MSG_TRUNC, is a malformed record with
// fdchan's rule as its check. Another error ends the sender with
// accounting-channel-failed.
func TestRecvErrorsEndSender(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		rule  string
		check string
	}{
		{"refusal", &fdchan.Refusal{Rule: fdchan.RuleRecordSize}, RuleRecordMalformed, string(fdchan.RuleRecordSize)},
		{"other", errors.New("boom"), RuleAccountingFailed, "recv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, buf := newState(t)
			s.Start("work", false)
			conn, end, r := startReader(t, s, NetdSource("work"))
			conn.push(hostversion.Record(own))
			conn.pushErr(tt.err)
			done(t, r)
			end.expect(t, tt.rule)
			expectLog(t, buf, `"rule":"`+tt.rule+`"`, `"check":"`+tt.check+`"`)
		})
	}
}

// TestVersionSendFails: a version record that can't be sent ends the
// sender.
func TestVersionSendFails(t *testing.T) {
	s, _, _ := newState(t)
	conn, end := newFakeConn(), newEnder()
	conn.sendErr = errors.New("broken")
	done(t, s.Read(conn, NetdSource("work"), end, own))
	end.expect(t, RuleAccountingFailed)
}
