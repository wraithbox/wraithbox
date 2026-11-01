package eventchan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// fakeConn is a channel whose writes the test controls.
type fakeConn struct {
	mu      sync.Mutex
	sent    [][]byte
	fail    int           // the next fail writes fail with ENOBUFS
	hold    chan struct{} // when not nil, a write waits for it to close
	delay   time.Duration // each write takes this long
	notices chan []byte
	closed  chan struct{}
	once    sync.Once
}

func newFakeConn() *fakeConn {
	return &fakeConn{notices: make(chan []byte, 8), closed: make(chan struct{})}
}

func (c *fakeConn) Send(record []byte, _ syscall.Conn) error {
	c.mu.Lock()
	hold, delay := c.hold, c.delay
	c.mu.Unlock()
	if hold != nil {
		<-hold
	}
	time.Sleep(delay)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail > 0 {
		c.fail--
		return syscall.ENOBUFS
	}
	c.sent = append(c.sent, bytes.Clone(record))
	return nil
}

func (c *fakeConn) Recv() (*fdchan.Message, error) {
	select {
	case b := <-c.notices:
		return &fdchan.Message{Record: b}, nil
	case <-c.closed:
		return nil, io.EOF
	}
}

func (c *fakeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeConn) records(t *testing.T) []*auditv1.EventRecord {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*auditv1.EventRecord, 0, len(c.sent))
	for _, b := range c.sent {
		r := &auditv1.EventRecord{}
		if err := proto.Unmarshal(b, r); err != nil {
			t.Fatalf("sent record doesn't decode: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// clock is a replaceable clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// logBuf is a concurrent log buffer.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var (
	work     = VM{Slot: auditv1.Slot_SLOT_WORK, Generation: 3}
	isolated = VM{Slot: auditv1.Slot_SLOT_ISOLATED, Generation: 1}
	t0       = time.Date(2026, 11, 1, 14, 12, 30, 0, time.UTC)
	net4001  = auditv1.EventClass_EVENT_CLASS_NETWORK_ACTIVITY
)

type harness struct {
	s    *Sender
	conn *fakeConn
	clk  *clock
	tick chan time.Time
	log  *logBuf
}

func start(t *testing.T, cfg Config) *harness {
	t.Helper()
	h := &harness{conn: newFakeConn(), clk: &clock{now: t0}, tick: make(chan time.Time), log: &logBuf{}}
	if cfg.Conn == nil {
		cfg.Conn = h.conn
	}
	cfg.Logger = slog.New(slog.NewTextHandler(h.log, nil))
	cfg.Now = h.clk.Now
	cfg.Tick = h.tick
	cfg.RetryEvery = time.Millisecond
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})
	return h
}

// eventually waits up to 5 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func event(rule string) *auditv1.Event {
	return &auditv1.Event{Class: net4001, Decision: "allow", Rule: rule, DestinationHost: "example.com", DestinationPort: 443}
}

func TestNewNeedsConnAndLogger(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New with no Conn: no error")
	}
	bad := VM{Slot: auditv1.Slot_SLOT_WORK}
	if _, err := New(Config{Conn: newFakeConn(), Logger: slog.New(slog.DiscardHandler), Own: &bad}); err == nil {
		t.Fatal("New with an own VM of generation 0: no error")
	}
}

func TestEmitWritesInOrder(t *testing.T) {
	h := start(t, Config{})
	for i := range 5 {
		h.s.Emit(work, event("rule-"+string(rune('a'+i))))
	}
	eventually(t, "five records", func() bool { return len(h.conn.records(t)) == 5 })
	for i, r := range h.conn.records(t) {
		if r.GetSequence() != uint64(i+1) || r.GetEvent().GetRule() != "rule-"+string(rune('a'+i)) {
			t.Errorf("record %d: sequence %d rule %q", i, r.GetSequence(), r.GetEvent().GetRule())
		}
		if r.GetVm().GetSlot() != work.Slot || r.GetVm().GetGeneration() != work.Generation {
			t.Errorf("record %d: VM %v", i, r.GetVm())
		}
	}
}

func TestEmitCutsGuestStrings(t *testing.T) {
	h := start(t, Config{})
	e := event("r")
	e.DestinationHost = strings.Repeat("h", 400)
	e.HttpPath = strings.Repeat("é", 1000)
	e.GuestLabel = &auditv1.GuestLabel{Program: "bad\xff bytes" + strings.Repeat("p", 300)}
	e.BytesIn = 1 << 60
	h.s.Emit(work, e)
	eventually(t, "the record", func() bool { return len(h.conn.records(t)) == 1 })
	got := h.conn.records(t)[0].GetEvent()
	if len(got.GetDestinationHost()) != MaxHost || len(got.GetHttpPath()) > MaxPath || len(got.GetGuestLabel().GetProgram()) > MaxLabel {
		t.Errorf("not cut: host %d path %d program %d", len(got.GetDestinationHost()), len(got.GetHttpPath()), len(got.GetGuestLabel().GetProgram()))
	}
	if got.GetBytesIn() != MaxBytes {
		t.Errorf("bytes in %d, want %d", got.GetBytesIn(), uint64(MaxBytes))
	}
}

// TestFailedWriteBlocksAndRetries: a write that fails with ENOBUFS
// blocks the channel, Admit refuses new work, and the record is written
// again, once, when wb-hostd reads (S04-08-records-and-bounds, "Event
// records").
func TestFailedWriteBlocksAndRetries(t *testing.T) {
	h := start(t, Config{})
	hold := make(chan struct{})
	h.conn.mu.Lock()
	h.conn.fail, h.conn.hold = 3, hold
	h.conn.mu.Unlock()
	h.s.Emit(work, event("first"))
	if r := h.s.Admit(work, net4001); r != "" {
		t.Fatalf("Admit before a failure: %q", r)
	}
	close(hold)
	eventually(t, "the block", func() bool { return strings.Contains(h.log.String(), "event channel blocked") })
	eventually(t, "the record", func() bool { return len(h.conn.records(t)) == 1 })
	eventually(t, "the unblock", func() bool { return h.s.Admit(work, net4001) == "" })
	got := h.conn.records(t)
	if len(got) != 1 || got[0].GetEvent().GetRule() != "first" || got[0].GetSequence() != 1 {
		t.Fatalf("records %v", got)
	}
	for _, want := range []string{"rule=event-channel-blocked decision=refuse", "no buffer space", "decision=admit"} {
		if !strings.Contains(h.log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, h.log)
		}
	}
}

func TestAdmitRefusesWhileBlocked(t *testing.T) {
	h := start(t, Config{})
	h.s.mu.Lock()
	h.s.markBlockedLocked("test")
	h.s.mu.Unlock()
	if r := h.s.Admit(isolated, net4001); r != RuleBlocked {
		t.Fatalf("Admit while blocked: %q, want %q", r, RuleBlocked)
	}
}

// TestSlowWriteBlocks: a write that takes longer than BlockAfter blocks
// the channel while it is in flight, and after it.
func TestSlowWriteBlocks(t *testing.T) {
	h := start(t, Config{})
	h.conn.mu.Lock()
	h.conn.delay = BlockAfter + 100*time.Millisecond
	h.conn.mu.Unlock()
	h.s.Emit(work, event("slow"))
	h.s.Emit(work, event("next"))
	eventually(t, "a refusal during the write", func() bool { return h.s.Admit(work, net4001) == RuleBlocked })
	if len(h.conn.records(t)) != 0 {
		t.Fatal("the slow write ended before the refusal")
	}
	h.conn.mu.Lock()
	h.conn.delay = 0
	h.conn.mu.Unlock()
	eventually(t, "the unblock", func() bool { return h.s.Admit(work, net4001) == "" })
	if !strings.Contains(h.log.String(), "more than 100ms") {
		t.Errorf("log lacks the reason:\n%s", h.log)
	}
}

// TestQueueCapStallsInput: at the cap, WaitRoom blocks until the queue
// is empty, and no record is dropped (#177).
func TestQueueCapStallsInput(t *testing.T) {
	h := start(t, Config{QueueCap: 4})
	hold := make(chan struct{})
	h.conn.mu.Lock()
	h.conn.hold = hold
	h.conn.mu.Unlock()
	for range 6 {
		h.s.Emit(work, event("r"))
	}
	if r := h.s.Admit(isolated, net4001); r != RuleBlocked {
		t.Fatalf("Admit at the cap: %q, want %s", r, RuleBlocked)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := h.s.WaitRoom(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitRoom at the cap: %v, want a deadline", err)
	}
	done := make(chan error, 1)
	go func() { done <- h.s.WaitRoom(context.Background()) }()
	close(hold)
	if err := <-done; err != nil {
		t.Fatalf("WaitRoom after the drain: %v", err)
	}
	if n := len(h.conn.records(t)); n != 6 {
		t.Fatalf("%d records written, want 6", n)
	}
	if err := h.s.WaitRoom(context.Background()); err != nil {
		t.Fatalf("WaitRoom with room: %v", err)
	}
	if r := h.s.Admit(isolated, net4001); r != "" {
		t.Fatalf("Admit after the drain: %q", r)
	}
	for _, want := range []string{"decision=stall", "decision=resume"} {
		if !strings.Contains(h.log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, h.log)
		}
	}
}

// truncatedConn reads fdchan's refusal that closes the channel, on every
// Recv, as fdchan returns it.
type truncatedConn struct {
	*fakeConn
	mu    sync.Mutex
	reads int
}

func (c *truncatedConn) Recv() (*fdchan.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return nil, &fdchan.Refusal{Rule: fdchan.RuleControlTruncated}
}

// TestControlTruncatedEndsReads: after fdchan's refusal that closes the
// channel, the sender reads no more notices, rather than spin, and
// still writes its records.
func TestControlTruncatedEndsReads(t *testing.T) {
	c := &truncatedConn{fakeConn: newFakeConn()}
	h := start(t, Config{Conn: c})
	eventually(t, "the log line", func() bool { return strings.Contains(h.log.String(), "rule=fdchan-control-truncated") })
	time.Sleep(20 * time.Millisecond)
	c.mu.Lock()
	reads := c.reads
	c.mu.Unlock()
	if reads != 1 {
		t.Fatalf("%d reads, want 1", reads)
	}
	h.s.Emit(work, event("r"))
	eventually(t, "the record", func() bool { return len(c.records(t)) == 1 })
}

func noticeBytes(vm VM, rule auditv1.BudgetRule, until time.Time) []byte {
	b, err := proto.Marshal(&auditv1.BudgetNotice{Vm: vm.proto(), Rule: rule, UntilUnix: until.Unix()})
	if err != nil {
		panic(err)
	}
	return b
}

// TestBudgetNoticeRefusesAndCounts: a notice refuses new work of its VM
// until its end, the refusals are one run record per minute, and the
// other VM is admitted (S09-11-audit, "Events per minute").
func TestBudgetNoticeRefusesAndCounts(t *testing.T) {
	h := start(t, Config{})
	end := t0.Truncate(time.Minute).Add(time.Minute)
	h.conn.notices <- noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE, end)
	eventually(t, "the notice", func() bool { return h.s.Admit(work, net4001) == RuleBudget })
	for range 9 {
		if r := h.s.Admit(work, net4001); r != RuleBudget {
			t.Fatalf("Admit under the notice: %q", r)
		}
	}
	if r := h.s.Admit(isolated, net4001); r != "" {
		t.Fatalf("Admit of the other VM: %q", r)
	}
	h.clk.set(end)
	if r := h.s.Admit(work, net4001); r != "" {
		t.Fatalf("Admit after the end: %q", r)
	}
	h.tick <- end
	eventually(t, "the run record", func() bool { return len(h.conn.records(t)) == 1 })
	run := h.conn.records(t)[0].GetRun()
	if run.GetRule() != string(RuleBudget) || run.GetDecision() != "refuse" || run.GetWindowSeconds() != 60 || run.GetOpen() {
		t.Fatalf("run %v", run)
	}
	// An Admit before the notice arrived admitted and counted nothing.
	// The refusals are the one that ended the wait and the nine after.
	if run.GetCount() != 10 {
		t.Fatalf("run count %d, want 10", run.GetCount())
	}
	if !strings.Contains(h.log.String(), "rule=audit-budget decision=refuse") {
		t.Errorf("log lacks the notice:\n%s", h.log)
	}
}

func TestBytesNoticeRefuses(t *testing.T) {
	h := start(t, Config{})
	h.conn.notices <- noticeBytes(isolated, auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, t0.Add(9*time.Hour))
	eventually(t, "the notice", func() bool { return h.s.Admit(isolated, net4001) == RuleBytes })
}

// TestInvalidNoticeIgnored: a notice that fails a check is logged with
// its rule and changes nothing. A notice is advice.
func TestInvalidNoticeIgnored(t *testing.T) {
	own := work
	h := start(t, Config{Own: &own})
	far := t0.Add(48 * time.Hour)
	for _, b := range [][]byte{
		{0xff, 0xff},
		noticeBytes(isolated, auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE, t0.Add(time.Minute)),
		noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_UNSPECIFIED, t0.Add(time.Minute)),
		noticeBytes(work, auditv1.BudgetRule(9), t0.Add(time.Minute)),
		noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, far),
		noticeBytes(VM{Slot: auditv1.Slot_SLOT_WORK}, auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, t0.Add(time.Minute)),
		append(noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, t0.Add(time.Minute)), 0x78, 0x01),
	} {
		h.conn.notices <- b
	}
	want := 7
	eventually(t, "the refusals", func() bool {
		return strings.Count(h.log.String(), "rule=budget-notice-invalid decision=ignore") == want
	})
	if r := h.s.Admit(work, net4001); r != "" {
		t.Fatalf("Admit after invalid notices: %q", r)
	}
}

func TestLaterNoticeWins(t *testing.T) {
	h := start(t, Config{})
	h.conn.notices <- noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, t0.Add(time.Hour))
	h.conn.notices <- noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE, t0.Add(time.Minute))
	eventually(t, "both notices", func() bool { return strings.Count(h.log.String(), "budget notice accepted") == 1 })
	h.clk.set(t0.Add(2 * time.Minute))
	if r := h.s.Admit(work, net4001); r != RuleBytes {
		t.Fatalf("Admit: %q, want the later notice's %q", r, RuleBytes)
	}
}

// TestEndVMWritesOpenRuns: EndVM writes each open run of the VM, for
// wb-hostd's stop record, and keeps the other VM's (S09-11-audit, "Runs
// with a count").
func TestEndVMWritesOpenRuns(t *testing.T) {
	h := start(t, Config{})
	for range 3 {
		h.s.CountRun(work, net4001, "refuse", "dns-rate")
	}
	h.s.CountRun(isolated, net4001, "refuse", "dns-rate")
	h.clk.set(t0.Add(10 * time.Second))
	h.s.EndVM(work)
	eventually(t, "the open run", func() bool { return len(h.conn.records(t)) == 1 })
	run := h.conn.records(t)[0].GetRun()
	if !run.GetOpen() || run.GetCount() != 3 || run.GetWindowSeconds() != 40 || run.GetRule() != "dns-rate" {
		t.Fatalf("run %v", run)
	}
	h.clk.set(t0.Add(time.Minute))
	h.tick <- t0.Add(time.Minute)
	eventually(t, "the other VM's run", func() bool { return len(h.conn.records(t)) == 2 })
	if got := h.conn.records(t)[1]; got.GetVm().GetSlot() != isolated.Slot || got.GetRun().GetOpen() {
		t.Fatalf("record %v", got)
	}
}

func TestCountRunNewMinuteWritesTheOld(t *testing.T) {
	h := start(t, Config{})
	h.s.CountRun(work, net4001, "refuse", "dns-rate")
	h.clk.set(t0.Add(time.Minute))
	h.s.CountRun(work, net4001, "refuse", "dns-rate")
	eventually(t, "the first minute's run", func() bool { return len(h.conn.records(t)) == 1 })
	if run := h.conn.records(t)[0].GetRun(); run.GetCount() != 1 || run.GetWindowSeconds() != 60 {
		t.Fatalf("run %v", run)
	}
}

func TestCountRunOfBadFormKeepsCount(t *testing.T) {
	h := start(t, Config{})
	h.s.CountRun(work, net4001, "Not A Decision", "rule")
	h.s.EndVM(work)
	eventually(t, "the run", func() bool { return len(h.conn.records(t)) == 1 })
	if run := h.conn.records(t)[0].GetRun(); run.GetRule() != string(RuleNotEncoded) || run.GetCount() != 1 {
		t.Fatalf("run %v", run)
	}
	if !strings.Contains(h.log.String(), "rule=event-record-not-encoded") {
		t.Errorf("log lacks the rule:\n%s", h.log)
	}
}

// TestEmitOfBadFormCounts: an event whose class, decision, rule or port
// a record can't carry is counted as a run under
// event-record-not-encoded, and the channel goes on.
func TestEmitOfBadFormCounts(t *testing.T) {
	h := start(t, Config{})
	bad := []*auditv1.Event{event("r"), event("r"), event("Bad Rule"), event("r")}
	bad[0].Decision = strings.Repeat("x", 2*MaxRecord)
	bad[1].Class = auditv1.EventClass(9999)
	bad[3].DestinationPort = MaxPort + 1
	for _, e := range bad {
		h.s.Emit(work, e)
	}
	if r := h.s.Admit(work, net4001); r != "" {
		t.Fatalf("Admit after a bad form: %q", r)
	}
	h.s.Emit(work, event("next"))
	h.s.EndVM(work)
	eventually(t, "the records", func() bool { return len(h.conn.records(t)) == 3 })
	recs := h.conn.records(t)
	if recs[0].GetEvent().GetRule() != "next" || recs[0].GetSequence() != 1 {
		t.Fatalf("first record %v", recs[0])
	}
	counts := map[auditv1.EventClass]uint64{}
	for _, r := range recs[1:] {
		run := r.GetRun()
		if run.GetRule() != string(RuleNotEncoded) || run.GetDecision() != "refuse" || !run.GetOpen() {
			t.Fatalf("run %v", run)
		}
		counts[run.GetClass()] += run.GetCount()
	}
	if counts[net4001] != 3 || counts[auditv1.EventClass_EVENT_CLASS_DETECTION_FINDING] != 1 {
		t.Fatalf("counts %v, want 3 of 4001 and 1 of 2004", counts)
	}
	if !strings.Contains(h.log.String(), "rule=event-record-not-encoded") {
		t.Errorf("log lacks the rule:\n%s", h.log)
	}
}

// TestUnencodedRecordRefusesForGood: a record that doesn't encode
// within MaxRecord refuses all new work, as nothing may run without its
// record (SEC10-audit).
func TestUnencodedRecordRefusesForGood(t *testing.T) {
	h := start(t, Config{})
	e := event("r")
	e.HttpPath = strings.Repeat("x", 2*MaxRecord) // past the bound Emit would cut it to
	h.s.mu.Lock()
	h.s.enqueueLocked(&auditv1.EventRecord{Vm: work.proto(), Kind: &auditv1.EventRecord_Event{Event: e}})
	h.s.mu.Unlock()
	if r := h.s.Admit(work, net4001); r != RuleBlocked {
		t.Fatalf("Admit after a record that didn't encode: %q", r)
	}
	h.s.Emit(work, event("next"))
	eventually(t, "the next record", func() bool { return len(h.conn.records(t)) == 1 })
	if got := h.conn.records(t)[0]; got.GetSequence() != 1 {
		t.Fatalf("sequence %d, want 1: the record that didn't encode takes no number", got.GetSequence())
	}
	if r := h.s.Admit(work, net4001); r != RuleBlocked {
		t.Fatalf("Admit later: %q", r)
	}
}

func TestCloseDrains(t *testing.T) {
	h := start(t, Config{})
	h.conn.mu.Lock()
	h.conn.fail = 5
	h.conn.mu.Unlock()
	h.s.Emit(work, event("r"))
	if err := h.s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.conn.records(t)); n != 1 {
		t.Fatalf("%d records after Close, want 1", n)
	}
}

func TestCloseTimesOutAndLogs(t *testing.T) {
	h := start(t, Config{})
	hold := make(chan struct{})
	h.conn.mu.Lock()
	h.conn.hold = hold
	h.conn.mu.Unlock()
	h.s.Emit(work, event("r"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	go func() { time.Sleep(50 * time.Millisecond); close(hold) }()
	if err := h.s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(h.log.String(), "decision=drop records=1") {
		t.Errorf("log lacks the drop:\n%s", h.log)
	}
}

func TestCut(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"aé", 2, "a"},
		{"a\xff", 8, "a" + string(utf8.RuneError)},
		{"", 0, ""},
	} {
		if got := Cut(tc.in, tc.n); got != tc.want {
			t.Errorf("Cut(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestValidForms(t *testing.T) {
	for s, want := range map[string]bool{"refuse": true, "": false, "Allow": false, "a1": false, strings.Repeat("a", 33): false} {
		if ValidDecision(s) != want {
			t.Errorf("ValidDecision(%q) != %v", s, want)
		}
	}
	for s, want := range map[string]bool{"dns-rate": true, "rule-2": true, "": false, "a_b": false, strings.Repeat("a", 65): false} {
		if ValidRule(s) != want {
			t.Errorf("ValidRule(%q) != %v", s, want)
		}
	}
}

// TestLargestRecordFits: the largest event and run the bounds allow
// encode within MaxRecord.
func TestLargestRecordFits(t *testing.T) {
	long := func(n int) string { return strings.Repeat("\U0010FFFF", n/4) }
	big := &auditv1.EventRecord{
		Sequence: ^uint64(0), Vm: &auditv1.VM{Slot: auditv1.Slot_SLOT_ISOLATED, Generation: ^uint64(0)},
		Kind: &auditv1.EventRecord_Event{Event: &auditv1.Event{
			Class: auditv1.EventClass_EVENT_CLASS_HTTP_ACTIVITY, Decision: strings.Repeat("a", MaxDecision),
			Rule: strings.Repeat("a", MaxRule), DestinationHost: long(MaxHost), DestinationPort: MaxPort,
			HttpMethod: long(MaxMethod), HttpPath: long(MaxPath), BytesIn: MaxBytes, BytesOut: MaxBytes,
			GuestLabel: &auditv1.GuestLabel{Project: long(MaxLabel), Session: long(MaxLabel), User: long(MaxLabel), Program: long(MaxLabel)},
		}},
	}
	b, err := proto.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > MaxRecord {
		t.Fatalf("largest event is %d bytes, more than %d", len(b), MaxRecord)
	}
	t.Logf("largest event: %d bytes", len(b))
}

func FuzzBudgetNotice(f *testing.F) {
	f.Add(noticeBytes(work, auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE, t0.Add(time.Minute)))
	f.Add(noticeBytes(isolated, auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY, t0.Add(time.Hour)))
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0x00, 0x10, 0x09})
	own := work
	f.Fuzz(func(t *testing.T, record []byte) {
		for _, o := range []*VM{nil, &own} {
			n, ref := checkNotice(record, o, t0)
			if ref == nil {
				checkAcceptedNotice(t, n, o)
			} else if ref.Error() == "" {
				t.Fatal("refusal with no reason")
			}
		}
	})
}

// checkAcceptedNotice fails t unless n is a notice a sender of own may
// act on.
func checkAcceptedNotice(t *testing.T, n notice, own *VM) {
	t.Helper()
	if _, ok := vmOf(n.vm.proto()); !ok {
		t.Fatalf("accepted a notice of no VM: %v", n.vm)
	}
	if own != nil && n.vm != *own {
		t.Fatalf("accepted %v for a sender of %v", n.vm, *own)
	}
	if n.rule != RuleBudget && n.rule != RuleBytes {
		t.Fatalf("accepted rule %q", n.rule)
	}
	if n.until.After(t0.Add(maxNoticeAhead)) {
		t.Fatalf("accepted an end %s ahead", n.until.Sub(t0))
	}
}
