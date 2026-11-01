package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// fakeConn hands Recv the queued messages, then io.EOF once closed for
// input. It records what is sent.
type fakeConn struct {
	in      chan received
	mu      sync.Mutex
	sent    [][]byte
	sendErr error
	closed  bool
}

type received struct {
	m   *fdchan.Message
	err error
}

func newFakeConn() *fakeConn { return &fakeConn{in: make(chan received, 16)} }

func (c *fakeConn) push(record []byte) { c.in <- received{m: &fdchan.Message{Record: record}} }

func (c *fakeConn) Recv() (*fdchan.Message, error) {
	r, ok := <-c.in
	if !ok {
		return nil, io.EOF
	}
	return r.m, r.err
}

func (c *fakeConn) Send(record []byte, _ syscall.Conn) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sendErr != nil {
		return c.sendErr
	}
	c.sent = append(c.sent, bytes.Clone(record))
	return nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) notices(t *testing.T) []*auditv1.BudgetNotice {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*auditv1.BudgetNotice
	for _, b := range c.sent {
		n := &auditv1.BudgetNotice{}
		if err := proto.Unmarshal(b, n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// fakeLog is an audit writer that keeps the records, with a set budget.
type fakeLog struct {
	mu      sync.Mutex
	records []audit.Record
	budget  audit.Budget
}

func (l *fakeLog) Record(r audit.Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r)
}

func (l *fakeLog) Budget(string) audit.Budget {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.budget
}

func (l *fakeLog) got() []audit.Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]audit.Record(nil), l.records...)
}

func (l *fakeLog) setBudget(b audit.Budget) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.budget = b
}

// ender records the rules it ended its sender with.
type ender struct {
	mu    sync.Mutex
	rules []string
}

func (e *ender) End(rule string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, rule)
}

func (e *ender) got() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.rules...)
}

var (
	work     = eventchan.VM{Slot: auditv1.Slot_SLOT_WORK, Generation: 2}
	isolated = eventchan.VM{Slot: auditv1.Slot_SLOT_ISOLATED, Generation: 5}
)

func pvm(vm eventchan.VM) *auditv1.VM {
	return &auditv1.VM{Slot: vm.Slot, Generation: vm.Generation}
}

func validEvent() *auditv1.Event {
	return &auditv1.Event{
		Class: auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY, Decision: "allow", Rule: "egress-allow",
		DestinationHost: "github.com", DestinationPort: 443,
		GuestLabel: &auditv1.GuestLabel{Project: "p", Program: "git"},
	}
}

func eventRecord(seq uint64, vm eventchan.VM, e *auditv1.Event) *auditv1.EventRecord {
	return &auditv1.EventRecord{Sequence: seq, Vm: pvm(vm), Kind: &auditv1.EventRecord_Event{Event: e}}
}

func runRecord(seq uint64, vm eventchan.VM, open bool) *auditv1.EventRecord {
	return &auditv1.EventRecord{Sequence: seq, Vm: pvm(vm), Kind: &auditv1.EventRecord_Run{Run: &auditv1.Run{
		Class: auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY, Decision: "refuse", Rule: "dns-rate",
		Count: 3, WindowSeconds: 60, Open: open,
	}}}
}

func marshal(t testing.TB, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type setup struct {
	hub  *Hub
	log  *fakeLog
	logs *bytes.Buffer
	mu   *sync.Mutex
}

func newSetup() *setup {
	s := &setup{log: &fakeLog{}, logs: &bytes.Buffer{}, mu: &sync.Mutex{}}
	s.hub = NewHub(s.log, slog.New(slog.NewTextHandler(lockedWriter{s.mu, s.logs}, nil)))
	s.hub.Begin(work)
	s.hub.Begin(isolated)
	return s
}

func (s *setup) logText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logs.String()
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// waitDone waits for r to stop.
func waitDone(t testing.TB, r *Receiver) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver didn't stop")
	}
}

func TestAcceptsRecords(t *testing.T) {
	s := newSetup()
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	conn.push(marshal(t, runRecord(2, work, false)))
	close(conn.in)
	waitDone(t, r)
	got := s.log.got()
	if len(got) != 2 || len(end.got()) != 0 {
		t.Fatalf("records %d, ends %v", len(got), end.got())
	}
	f := got[0].Fields()
	if got[0].Slot != "work" || got[0].Generation != 2 || f.Channel.Sender != "wb-netd" || f.Channel.Sequence != 1 ||
		f.GuestLabel.Program != "git" || got[0].ClassUID() != audit.ClassNetworkActivity {
		t.Errorf("event record %+v %+v", got[0], f)
	}
	if run := got[1].Fields().Run; run == nil || run.Count != 3 {
		t.Errorf("run record %+v", got[1].Fields())
	}
	if !strings.Contains(s.logText(), "event channel closed by its sender") {
		t.Errorf("log lacks the close:\n%s", s.logText())
	}
}

// refusedCases are records that end their sender, each with what makes
// it fail. Each is the first record of a wb-netd of the work VM, or of
// wb-proxyd when proxyd is set.
// unknown200 is field 200, a varint of 1, which no message of the
// contract has.
var unknown200 = protoreflect.RawFields{0xc0, 0x0c, 0x01}

func refusedCases(t testing.TB) []struct {
	name   string
	proxyd bool
	record []byte
} {
	t.Helper()
	ev := func(change func(e *auditv1.Event)) []byte {
		e := validEvent()
		change(e)
		return marshal(t, eventRecord(1, work, e))
	}
	run := func(change func(r *auditv1.Run)) []byte {
		r := runRecord(1, work, false)
		change(r.GetRun())
		return marshal(t, r)
	}
	long := func(n int) string { return strings.Repeat("a", n+1) }
	return []struct {
		name   string
		proxyd bool
		record []byte
	}{
		{"doesn't decode", false, []byte{0xff, 0xff, 0xff}},
		{"unknown field", false, append(marshal(t, eventRecord(1, work, validEvent())), 0x78, 0x01)},
		{"unknown field in the event", false, ev(func(e *auditv1.Event) { e.ProtoReflect().SetUnknown(unknown200) })},
		{"unknown field in the label", false, ev(func(e *auditv1.Event) { e.GetGuestLabel().ProtoReflect().SetUnknown(unknown200) })},
		{"unknown field in the run", false, run(func(r *auditv1.Run) { r.ProtoReflect().SetUnknown(unknown200) })},
		{"unknown field in the VM", false, marshal(t, func() *auditv1.EventRecord {
			r := eventRecord(1, work, validEvent())
			r.GetVm().ProtoReflect().SetUnknown(unknown200)
			return r
		}())},
		{"sequence 2 first", false, marshal(t, eventRecord(2, work, validEvent()))},
		{"sequence 0", false, marshal(t, eventRecord(0, work, validEvent()))},
		{"no VM", false, marshal(t, &auditv1.EventRecord{Sequence: 1, Kind: &auditv1.EventRecord_Event{Event: validEvent()}})},
		{"generation 0", false, marshal(t, eventRecord(1, eventchan.VM{Slot: auditv1.Slot_SLOT_WORK}, validEvent()))},
		{"unknown slot", false, marshal(t, eventRecord(1, eventchan.VM{Slot: 9, Generation: 2}, validEvent()))},
		{"netd names the other VM", false, marshal(t, eventRecord(1, isolated, validEvent()))},
		{"netd names an old run", false, marshal(t, eventRecord(1, eventchan.VM{Slot: work.Slot, Generation: 1}, validEvent()))},
		{"proxyd names a VM not running", true, marshal(t, eventRecord(1, eventchan.VM{Slot: work.Slot, Generation: 9}, validEvent()))},
		{"no kind", false, marshal(t, &auditv1.EventRecord{Sequence: 1, Vm: pvm(work)})},
		{"unknown class", false, ev(func(e *auditv1.Event) { e.Class = 4003 })},
		{"no class", false, ev(func(e *auditv1.Event) { e.Class = 0 })},
		{"empty decision", false, ev(func(e *auditv1.Event) { e.Decision = "" })},
		{"decision upper case", false, ev(func(e *auditv1.Event) { e.Decision = "Allow" })},
		{"rule too long", false, ev(func(e *auditv1.Event) { e.Rule = long(eventchan.MaxRule) })},
		{"rule with a space", false, ev(func(e *auditv1.Event) { e.Rule = "a rule" })},
		{"host too long", false, ev(func(e *auditv1.Event) { e.DestinationHost = long(eventchan.MaxHost) })},
		{"port too large", false, ev(func(e *auditv1.Event) { e.DestinationPort = 65536 })},
		{"method too long", false, ev(func(e *auditv1.Event) { e.HttpMethod = long(eventchan.MaxMethod) })},
		{"path too long", false, ev(func(e *auditv1.Event) { e.HttpPath = long(eventchan.MaxPath) })},
		{"bytes in too many", false, ev(func(e *auditv1.Event) { e.BytesIn = eventchan.MaxBytes + 1 })},
		{"bytes out too many", false, ev(func(e *auditv1.Event) { e.BytesOut = eventchan.MaxBytes + 1 })},
		{"label project too long", false, ev(func(e *auditv1.Event) { e.GuestLabel.Project = long(eventchan.MaxLabel) })},
		{"label session too long", false, ev(func(e *auditv1.Event) { e.GuestLabel.Session = long(eventchan.MaxLabel) })},
		{"label user too long", false, ev(func(e *auditv1.Event) { e.GuestLabel.User = long(eventchan.MaxLabel) })},
		{"label program too long", false, ev(func(e *auditv1.Event) { e.GuestLabel.Program = long(eventchan.MaxLabel) })},
		{"run count 0", false, run(func(r *auditv1.Run) { r.Count = 0 })},
		{"run count too large", false, run(func(r *auditv1.Run) { r.Count = eventchan.MaxRunCount + 1 })},
		{"run window 0", false, run(func(r *auditv1.Run) { r.WindowSeconds = 0 })},
		{"run window too long", false, run(func(r *auditv1.Run) { r.WindowSeconds = eventchan.MaxRunWindow + 1 })},
		{"run of no class", false, run(func(r *auditv1.Run) { r.Class = 0 })},
		{"run rule empty", false, run(func(r *auditv1.Run) { r.Rule = "" })},
	}
}

// TestRefusedRecordEndsSender feeds the receiver each kind of bad
// record and sees it end the sender with event-record-invalid, write
// nothing, and read no further (S04-08-records-and-bounds, "Event
// records").
func TestRefusedRecordEndsSender(t *testing.T) {
	for _, tc := range refusedCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			s := newSetup()
			src := Netd(work)
			if tc.proxyd {
				src = Proxyd()
			}
			conn, end := newFakeConn(), &ender{}
			r := s.hub.Attach(conn, src, end)
			conn.push(tc.record)
			conn.push(marshal(t, eventRecord(1, work, validEvent())))
			waitDone(t, r)
			checkEndedAtFirst(t, s, conn, end)
		})
	}
}

// checkEndedAtFirst fails t unless the receiver ended its sender with
// event-record-invalid at the first record, wrote nothing, read no
// further, closed the channel, and logged the decision.
func checkEndedAtFirst(t *testing.T, s *setup, conn *fakeConn, end *ender) {
	t.Helper()
	if got := end.got(); len(got) != 1 || got[0] != RuleRecordInvalid {
		t.Fatalf("ends %v, want [%s]", got, RuleRecordInvalid)
	}
	if n := len(s.log.got()); n != 0 {
		t.Fatalf("%d records written", n)
	}
	if len(conn.in) != 1 {
		t.Fatal("the receiver read past the refused record")
	}
	if !conn.isClosed() {
		t.Fatal("the channel isn't closed")
	}
	if !strings.Contains(s.logText(), "rule=event-record-invalid decision=end-sender") {
		t.Errorf("log lacks the decision:\n%s", s.logText())
	}
}

func TestRepeatedSequenceEndsSender(t *testing.T) {
	s := newSetup()
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	waitDone(t, r)
	if len(s.log.got()) != 1 || len(end.got()) != 1 {
		t.Fatalf("records %d, ends %v", len(s.log.got()), end.got())
	}
}

func TestDescriptorEndsSender(t *testing.T) {
	s := newSetup()
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Proxyd(), end)
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	conn.in <- received{m: &fdchan.Message{Record: marshal(t, eventRecord(1, work, validEvent())), File: f}}
	waitDone(t, r)
	if got := end.got(); len(got) != 1 || got[0] != RuleRecordInvalid || len(s.log.got()) != 0 {
		t.Fatalf("ends %v, records %d", got, len(s.log.got()))
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the descriptor wasn't closed: %v", err)
	}
}

// TestRecvErrorsEndSender: a refusal of fdchan ends the sender with
// fdchan's rule, and another error with event-channel-failed.
func TestRecvErrorsEndSender(t *testing.T) {
	for _, tc := range []struct {
		err  error
		rule string
	}{
		{&fdchan.Refusal{Rule: fdchan.RuleRecordSize}, string(fdchan.RuleRecordSize)},
		{syscall.EIO, RuleChannelFailed},
	} {
		s := newSetup()
		conn, end := newFakeConn(), &ender{}
		r := s.hub.Attach(conn, Netd(work), end)
		conn.in <- received{err: tc.err}
		waitDone(t, r)
		if got := end.got(); len(got) != 1 || got[0] != tc.rule {
			t.Errorf("%v: ends %v, want %s", tc.err, got, tc.rule)
		}
	}
}

func TestEOFEndsNothing(t *testing.T) {
	s := newSetup()
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	close(conn.in)
	waitDone(t, r)
	if len(end.got()) != 0 {
		t.Fatalf("ends %v", end.got())
	}
}

// TestOpenRunsInStopRecord: open runs are held for the VM's stop record
// and returned by End, the other VM's kept (S09-11-audit, "Runs with a
// count").
func TestOpenRunsInStopRecord(t *testing.T) {
	s := newSetup()
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Proxyd(), end)
	conn.push(marshal(t, runRecord(1, work, true)))
	conn.push(marshal(t, runRecord(2, isolated, true)))
	close(conn.in)
	waitDone(t, r)
	if n := len(s.log.got()); n != 0 {
		t.Fatalf("%d records written, want the open runs held", n)
	}
	runs := s.hub.End(work)
	if len(runs) != 1 || runs[0].Count != 3 || runs[0].Sender != "wb-proxyd" || runs[0].Rule != "dns-rate" {
		t.Fatalf("runs %+v", runs)
	}
	if again := s.hub.End(work); len(again) != 0 {
		t.Fatalf("runs again %+v", again)
	}
	if other := s.hub.End(isolated); len(other) != 1 {
		t.Fatalf("other VM's runs %+v", other)
	}
}

func TestOpenRunsPastTheCapAreWritten(t *testing.T) {
	s := newSetup()
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	go func() {
		for i := range maxOpenRuns + 2 {
			conn.push(marshal(t, runRecord(uint64(i+1), work, true)))
		}
		close(conn.in)
	}()
	waitDone(t, r)
	if n := len(s.hub.End(work)); n != maxOpenRuns {
		t.Fatalf("%d runs held, want %d", n, maxOpenRuns)
	}
	got := s.log.got()
	if len(got) != 2 || !got[0].Fields().Run.Open {
		t.Fatalf("records %d", len(got))
	}
}

// TestEndForgetsTheVM: End forgets the notices sent for the VM, and an
// open run of the VM that arrives after End is written as its own
// record, not held for a stop record that was already written.
func TestEndForgetsTheVM(t *testing.T) {
	s := newSetup()
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Netd(work), end)
	s.log.setBudget(audit.Budget{Rule: audit.RuleBudget, Until: time.Date(2026, 11, 1, 12, 1, 0, 0, time.UTC)})
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	waitFor(t, func() bool { return len(conn.notices(t)) == 1 })
	s.hub.End(work)
	s.hub.mu.Lock()
	sent := len(r.sent)
	s.hub.mu.Unlock()
	if sent != 0 {
		t.Fatalf("%d notices kept after End", sent)
	}
	conn.push(marshal(t, runRecord(2, work, true)))
	close(conn.in)
	waitDone(t, r)
	got := s.log.got()
	if len(got) != 2 || !got[1].Fields().Run.Open || len(end.got()) != 0 {
		t.Fatalf("records %d, ends %v", len(got), end.got())
	}
	if runs := s.hub.End(work); len(runs) != 0 {
		t.Fatalf("%d open runs held after End", len(runs))
	}
}

// TestProxydNamesStoppedVM: once End marks a VM stopped, wb-proxyd may
// no longer name it.
func TestProxydNamesStoppedVM(t *testing.T) {
	s := newSetup()
	s.hub.End(work)
	conn, end := newFakeConn(), &ender{}
	r := s.hub.Attach(conn, Proxyd(), end)
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	waitDone(t, r)
	if len(end.got()) != 1 {
		t.Fatalf("ends %v", end.got())
	}
}

// TestBudgetNotices: over a budget, each sender of the VM gets one
// notice, the other VM's wb-netd none, and a new end sends again
// (S09-11-audit, "Budgets").
func TestBudgetNotices(t *testing.T) {
	s := newSetup()
	netdWork, netdIso, proxyd := newFakeConn(), newFakeConn(), newFakeConn()
	rWork := s.hub.Attach(netdWork, Netd(work), &ender{})
	rIso := s.hub.Attach(netdIso, Netd(isolated), &ender{})
	rProxy := s.hub.Attach(proxyd, Proxyd(), &ender{})
	until := time.Date(2026, 11, 1, 12, 1, 0, 0, time.UTC)
	s.log.setBudget(audit.Budget{Rule: audit.RuleBudget, Until: until})
	netdWork.push(marshal(t, eventRecord(1, work, validEvent())))
	netdWork.push(marshal(t, eventRecord(2, work, validEvent())))
	waitFor(t, func() bool { return len(s.log.got()) == 2 })
	for name, c := range map[string]*fakeConn{"netd of work": netdWork, "proxyd": proxyd} {
		n := c.notices(t)
		if len(n) != 1 || n[0].GetRule() != auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE ||
			n[0].GetUntilUnix() != until.Unix() || n[0].GetVm().GetSlot() != work.Slot {
			t.Errorf("%s: notices %v", name, n)
		}
	}
	if n := netdIso.notices(t); len(n) != 0 {
		t.Errorf("netd of isolated: notices %v", n)
	}
	s.log.setBudget(audit.Budget{Rule: audit.RuleBytes, Until: until.Add(12 * time.Hour)})
	netdWork.push(marshal(t, eventRecord(3, work, validEvent())))
	waitFor(t, func() bool { return len(proxyd.notices(t)) == 2 })
	if n := proxyd.notices(t)[1]; n.GetRule() != auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY {
		t.Errorf("second notice %v", n)
	}
	for _, c := range []*fakeConn{netdWork, netdIso, proxyd} {
		close(c.in)
	}
	for _, r := range []*Receiver{rWork, rIso, rProxy} {
		waitDone(t, r)
	}
	if !strings.Contains(s.logText(), "rule=audit-budget decision=refuse-new-work") {
		t.Errorf("log lacks the notice:\n%s", s.logText())
	}
}

func TestBudgetNoticeRetried(t *testing.T) {
	s := newSetup()
	conn := newFakeConn()
	conn.sendErr = syscall.ENOBUFS
	r := s.hub.Attach(conn, Netd(work), &ender{})
	s.log.setBudget(audit.Budget{Rule: audit.RuleBudget, Until: time.Unix(1_800_000_000, 0)})
	conn.push(marshal(t, eventRecord(1, work, validEvent())))
	waitFor(t, func() bool { return strings.Contains(s.logText(), "decision=retry") })
	conn.mu.Lock()
	conn.sendErr = nil
	conn.mu.Unlock()
	conn.push(marshal(t, eventRecord(2, work, validEvent())))
	waitFor(t, func() bool { return len(conn.notices(t)) == 1 })
	close(conn.in)
	waitDone(t, r)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

// TestBudgetWithWriter: with the audit writer on a fake clock at the
// default budget, the 6000th Network Activity record of a minute sends
// the work VM's senders a notice that ends at the minute's turn, and
// every record is written.
func TestBudgetWithWriter(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 30, 0, time.UTC)
	w, err := audit.New(audit.Config{
		Dir: t.TempDir(), DataDir: t.TempDir(), RetentionDays: 90, FloorMiB: 1, Version: "test",
		Logger: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }, Tick: make(chan time.Time),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	hub := NewHub(w, slog.New(slog.DiscardHandler))
	hub.Begin(work)
	conn := newFakeConn()
	r := hub.Attach(conn, Netd(work), &ender{})
	go func() {
		for i := range 6001 {
			conn.push(marshal(t, eventRecord(uint64(i+1), work, validEvent())))
		}
		close(conn.in)
	}()
	waitDone(t, r)
	n := conn.notices(t)
	if len(n) != 1 || n[0].GetUntilUnix() != time.Date(2026, 11, 1, 12, 1, 0, 0, time.UTC).Unix() {
		t.Fatalf("notices %v", n)
	}
	if b := w.Budget("work"); b.EventsThisMinute != 6001 || b.Rule != audit.RuleBudget {
		t.Fatalf("budget %+v", b)
	}
	if b := w.Budget("isolated"); b.Rule != "" {
		t.Fatalf("the other VM's budget %+v", b)
	}
}

// FuzzEventRecord feeds the receiver a record of any bytes, then a
// valid one. A refused record ends the sender and nothing after it is
// written. An accepted one is within its bounds and writes a record that
// marshals.
func FuzzEventRecord(f *testing.F) {
	f.Add(marshal(f, eventRecord(1, work, validEvent())), false)
	f.Add(marshal(f, runRecord(1, work, false)), true)
	f.Add(marshal(f, runRecord(1, isolated, true)), true)
	for _, tc := range refusedCases(f) {
		f.Add(tc.record, tc.proxyd)
	}
	f.Fuzz(func(t *testing.T, record []byte, proxyd bool) {
		s := newSetup()
		src := Netd(work)
		if proxyd {
			src = Proxyd()
		}
		conn, end := newFakeConn(), &ender{}
		r := s.hub.Attach(conn, src, end)
		conn.push(record)
		conn.push(marshal(t, eventRecord(2, work, validEvent())))
		close(conn.in)
		waitDone(t, r)
		got := s.log.got()
		if len(end.got()) > 0 {
			if len(got) != 0 {
				t.Fatalf("%d records written after a refusal", len(got))
			}
			return
		}
		for _, rec := range got {
			if _, err := json.Marshal(rec.Event); err != nil {
				t.Fatalf("record doesn't marshal: %v", err)
			}
		}
		assertAccepted(t, record, src)
	})
}

// assertAccepted checks, apart from check, what an accepted record
// holds: no unknown field, sequence 1, a VM the sender may name, a known
// class, and every field within its bound (proto/wraithbox/audit/v1).
func assertAccepted(t *testing.T, record []byte, src Source) {
	t.Helper()
	rec := &auditv1.EventRecord{}
	if err := proto.Unmarshal(record, rec); err != nil {
		t.Fatalf("accepted a record that doesn't decode: %v", err)
	}
	if n := unknownBytes(rec.ProtoReflect()); n != 0 || rec.GetSequence() != 1 {
		t.Fatalf("accepted %d unknown bytes, sequence %d", n, rec.GetSequence())
	}
	vm := eventchan.VM{Slot: rec.GetVm().GetSlot(), Generation: rec.GetVm().GetGeneration()}
	if src.VM != nil && vm != *src.VM || src.VM == nil && vm != work && vm != isolated {
		t.Fatalf("accepted %s from %s", vm, src.Sender)
	}
	if e := rec.GetEvent(); e != nil {
		assertEventBounds(t, e)
		return
	}
	r := rec.GetRun()
	if r == nil {
		t.Fatal("accepted a record of no kind")
	}
	assertHead(t, r.GetClass(), r.GetDecision(), r.GetRule())
	if r.GetCount() < 1 || r.GetCount() > eventchan.MaxRunCount || r.GetWindowSeconds() < 1 || r.GetWindowSeconds() > eventchan.MaxRunWindow {
		t.Fatalf("accepted run count %d window %d", r.GetCount(), r.GetWindowSeconds())
	}
}

func assertHead(t *testing.T, class auditv1.EventClass, decision, rule string) {
	t.Helper()
	if !eventchan.KnownClass(class) || !eventchan.ValidDecision(decision) || !eventchan.ValidRule(rule) {
		t.Fatalf("accepted class %d, decision %q, rule %q", class, decision, rule)
	}
}

func assertEventBounds(t *testing.T, e *auditv1.Event) {
	t.Helper()
	assertHead(t, e.GetClass(), e.GetDecision(), e.GetRule())
	l := e.GetGuestLabel()
	if len(e.GetDestinationHost()) > eventchan.MaxHost || len(e.GetHttpMethod()) > eventchan.MaxMethod ||
		len(e.GetHttpPath()) > eventchan.MaxPath || e.GetDestinationPort() > eventchan.MaxPort {
		t.Fatalf("accepted host %d, method %d, path %d bytes, port %d",
			len(e.GetDestinationHost()), len(e.GetHttpMethod()), len(e.GetHttpPath()), e.GetDestinationPort())
	}
	for _, s := range []string{l.GetProject(), l.GetSession(), l.GetUser(), l.GetProgram()} {
		if len(s) > eventchan.MaxLabel {
			t.Fatalf("accepted a label of %d bytes", len(s))
		}
	}
	if e.GetBytesIn() > eventchan.MaxBytes || e.GetBytesOut() > eventchan.MaxBytes {
		t.Fatalf("accepted bytes in %d out %d", e.GetBytesIn(), e.GetBytesOut())
	}
}
