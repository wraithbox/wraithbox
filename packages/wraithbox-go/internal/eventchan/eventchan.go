// Package eventchan is the sender's end of the event channel, which
// wb-netd and wb-proxyd use to send wb-hostd what goes to the audit log
// (S04-08-records-and-bounds, "Event records"; S09-11-audit). The record
// is EventRecord of proto/wraithbox/audit/v1, one per datagram on an
// inherited internal/platform/fdchan socket.
//
// The sender never drops a record, and never runs on without its
// records reaching wb-hostd (SEC10-audit):
//
//   - Admit says whether new work may start. Each daemon calls it before
//     a new lookup, connection, stream or request. It refuses with
//     event-channel-blocked while the channel is blocked, and with
//     audit-budget or audit-bytes while wb-hostd's last BudgetNotice for
//     the VM holds.
//   - Emit queues a record. One goroutine writes the queue in order. A
//     write that fails, with ENOBUFS or EAGAIN when wb-hostd doesn't read,
//     or that blocks for more than BlockAfter, marks the channel blocked.
//     The record stays at the head of the queue and is written again
//     until a write succeeds. The channel is blocked until the queue is
//     empty.
//   - At QueueCap records the sender stalls: WaitRoom blocks until the
//     queue is empty. The daemon calls it before it reads new input from
//     the guest, so the queue holds only records of work already
//     admitted, which the daemon's per-VM caps bound
//     (SEC13-bounded-resources).
//   - Admit's refusals, and the refusals the daemon aggregates per rule
//     (S07-02-packet-path, "Events and rate limits"), are counted as runs
//     with CountRun: one record per rule per minute, and the open runs of
//     a VM at EndVM (S09-11-audit, "Runs with a count").
package eventchan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// Rule names the rule behind a refusal, for the log and the run record.
type Rule string

// The rules of this package.
const (
	// RuleBlocked: a write of the event channel failed or blocked past
	// BlockAfter, and the queue hasn't drained since
	// (S04-08-records-and-bounds, "Event records").
	RuleBlocked Rule = "event-channel-blocked"
	// RuleBudget: the VM is over audit_events_per_minute until the UTC
	// minute turns (S09-11-audit, "Events per minute").
	RuleBudget Rule = "audit-budget"
	// RuleBytes: the VM is over audit_vm_bytes_per_day until the UTC day
	// turns (S09-11-audit, "Bytes per day").
	RuleBytes Rule = "audit-bytes"
	// RuleNoticeInvalid: a BudgetNotice from wb-hostd doesn't decode, names
	// a VM this sender doesn't serve, or ends too far ahead. It is logged
	// and has no effect.
	RuleNoticeInvalid Rule = "budget-notice-invalid"
	// RuleNotEncoded: a record doesn't encode within MaxRecord. The bounds
	// keep that from happening. If it does, the channel stays blocked
	// for good, as nothing may run without its record.
	RuleNotEncoded Rule = "event-record-not-encoded"
)

// BlockAfter is how long one write may take before the channel counts
// as blocked (S04-08-records-and-bounds, "Event records").
const BlockAfter = 100 * time.Millisecond

// DefaultQueueCap is the queue's cap: above the records the work a
// VM's caps admit can produce, so the cap stalls input and never drops a
// record (S04-08-records-and-bounds, "Event records"; #177). At 4 KiB a
// record, it is at most 16 MiB.
const DefaultQueueCap = 4096

// defaultRetryEvery is how long the writer waits before it writes a
// failed record again.
const defaultRetryEvery = 5 * time.Millisecond

// Conn is the channel: an *fdchan.Channel, or a fake in tests.
type Conn interface {
	Send(record []byte, fd syscall.Conn) error
	Recv() (*fdchan.Message, error)
	Close() error
}

// VM is one run of a VM, as a record names it.
type VM struct {
	Slot       auditv1.Slot
	Generation uint64
}

func (v VM) String() string {
	return fmt.Sprintf("%s/%d", slotName(v.Slot), v.Generation)
}

func (v VM) proto() *auditv1.VM { return &auditv1.VM{Slot: v.Slot, Generation: v.Generation} }

// SlotOf is the record's slot of a vmbundle slot name.
func SlotOf(slot string) auditv1.Slot {
	switch slot {
	case vmbundle.SlotWork:
		return auditv1.Slot_SLOT_WORK
	case vmbundle.SlotIsolated:
		return auditv1.Slot_SLOT_ISOLATED
	}
	return auditv1.Slot_SLOT_UNSPECIFIED
}

// slotName is the vmbundle slot name of a record's slot, or "" for none.
func slotName(s auditv1.Slot) string {
	switch s {
	case auditv1.Slot_SLOT_WORK:
		return vmbundle.SlotWork
	case auditv1.Slot_SLOT_ISOLATED:
		return vmbundle.SlotIsolated
	case auditv1.Slot_SLOT_UNSPECIFIED:
	}
	return ""
}

// SlotName is slotName for the receiver.
func SlotName(s auditv1.Slot) string { return slotName(s) }

// vmOf checks a record's VM: a known slot and a generation of at least 1.
func vmOf(v *auditv1.VM) (VM, bool) {
	vm := VM{Slot: v.GetSlot(), Generation: v.GetGeneration()}
	return vm, slotName(vm.Slot) != "" && vm.Generation >= 1
}

// VMOf is vmOf for the receiver.
func VMOf(v *auditv1.VM) (VM, bool) { return vmOf(v) }

// Config configures a Sender.
type Config struct {
	Conn   Conn
	Logger *slog.Logger
	// Own is the VM of a wb-netd, which takes notices for it only. nil
	// for wb-proxyd, which serves both VMs.
	Own *VM
	// Now is the clock of budgets and runs. nil means time.Now. The
	// BlockAfter check always uses the monotonic clock.
	Now func() time.Time
	// Tick drives the end of run windows. nil means a ticker of one
	// second.
	Tick <-chan time.Time
	// QueueCap is the queue's cap. 0 means DefaultQueueCap.
	QueueCap int
	// RetryEvery is how long the writer waits before it writes a failed
	// record again. 0 means 5 ms.
	RetryEvery time.Duration
}

// refusal is a budget notice in effect for a VM.
type refusal struct {
	rule  Rule
	until time.Time
}

// Sender is the sender's end of the channel. Its methods are safe for
// concurrent use.
type Sender struct {
	cfg Config
	log *slog.Logger

	mu sync.Mutex
	// queue holds the encoded records not yet written, the head first.
	queue [][]byte
	seq   uint64
	// blocked: a write failed or took longer than BlockAfter, and the
	// queue hasn't been empty since.
	blocked bool
	// broken: a record couldn't be encoded. Admit refuses for good.
	broken bool
	// sendingSince is when the write in flight began, zero when none is.
	sendingSince time.Time
	// stalled: the queue reached the cap, and hasn't been empty since.
	// drained is closed when it empties.
	stalled bool
	drained chan struct{}
	budgets map[VM]refusal
	runs    map[runKey]*run

	work chan struct{}
	quit chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// New starts a sender on cfg.Conn: the goroutine that writes the queue,
// the one that reads budget notices, and the one that ends run windows.
func New(cfg Config) (*Sender, error) {
	if cfg.Conn == nil || cfg.Logger == nil {
		return nil, errors.New("eventchan: Conn and Logger are required")
	}
	if cfg.Own != nil {
		if _, ok := vmOf(cfg.Own.proto()); !ok {
			return nil, fmt.Errorf("eventchan: own VM %s is not a VM", *cfg.Own)
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.QueueCap <= 0 {
		cfg.QueueCap = DefaultQueueCap
	}
	if cfg.RetryEvery <= 0 {
		cfg.RetryEvery = defaultRetryEvery
	}
	tick, stop := cfg.Tick, func() {}
	if tick == nil {
		t := time.NewTicker(time.Second)
		tick, stop = t.C, t.Stop
	}
	s := &Sender{
		cfg: cfg, log: cfg.Logger,
		budgets: map[VM]refusal{}, runs: map[runKey]*run{},
		work: make(chan struct{}, 1), quit: make(chan struct{}),
	}
	s.wg.Add(3)
	go s.writeLoop()
	go s.readLoop()
	go s.tickLoop(tick, stop)
	return s, nil
}

// Admit says whether new work of class for vm may start: "" when it
// may, and the rule that refuses it otherwise. A refusal is counted in
// the run of its rule. The daemon refuses the work with the rule, and
// writes no event of its own for it.
func (s *Sender) Admit(vm VM, class auditv1.EventClass) Rule {
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	rule := s.refusalLocked(vm, now)
	if rule != "" {
		s.countLocked(runKey{vm: vm, class: class, decision: "refuse", rule: string(rule)}, now)
	}
	return rule
}

func (s *Sender) refusalLocked(vm VM, now time.Time) Rule {
	if s.broken {
		return RuleBlocked
	}
	if !s.blocked && !s.sendingSince.IsZero() && time.Since(s.sendingSince) > BlockAfter {
		s.markBlockedLocked(fmt.Sprintf("a write has taken more than %s", BlockAfter))
	}
	// A queue at its cap admits nothing new until it drains (#477,
	// S04-08-records-and-bounds, "Event records").
	if s.blocked || s.stalled {
		return RuleBlocked
	}
	if r, ok := s.budgets[vm]; ok && now.Before(r.until) {
		return r.rule
	}
	return ""
}

// Emit queues the record of one event of vm. It never blocks and never
// drops a record. It cuts e's guest strings to their bounds, and takes
// e. An event whose class, decision, rule or port a record can't carry
// is counted as a run under event-record-not-encoded instead (formOf).
func (s *Sender) Emit(vm VM, e *auditv1.Event) {
	bound(e)
	k := runKey{vm: vm, class: e.GetClass(), decision: e.GetDecision(), rule: e.GetRule()}
	if f := s.formOf(k, e.GetDestinationPort()); f != k {
		now := s.cfg.Now()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.countLocked(f, now)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueueLocked(&auditv1.EventRecord{Vm: vm.proto(), Kind: &auditv1.EventRecord_Event{Event: e}})
}

func (s *Sender) enqueueLocked(rec *auditv1.EventRecord) {
	rec.Sequence = s.seq + 1
	b, err := proto.Marshal(rec)
	if err != nil || len(b) > MaxRecord {
		// The bounds keep every record under MaxRecord, so this is a bug.
		// Nothing may run without its record, so new work is refused for
		// good.
		s.broken = true
		s.log.Error("event record not sent", "rule", string(RuleNotEncoded), "decision", "refuse-all",
			"bytes", len(b), "max", MaxRecord, "error", fmt.Sprint(err))
		return
	}
	s.seq++
	s.queue = append(s.queue, b)
	if len(s.queue) >= s.cfg.QueueCap && !s.stalled {
		s.stalled = true
		s.drained = make(chan struct{})
		s.log.Warn("event queue full, input stalled", "rule", string(RuleBlocked), "decision", "stall",
			"queued", len(s.queue), "cap", s.cfg.QueueCap)
	}
	select {
	case s.work <- struct{}{}:
	default:
	}
}

// WaitRoom returns once the sender may read new input from the guest:
// at once unless the queue reached its cap, and then once it is empty.
// It returns ctx's error if ctx ends first.
func (s *Sender) WaitRoom(ctx context.Context) error {
	s.mu.Lock()
	stalled, drained := s.stalled, s.drained
	s.mu.Unlock()
	if !stalled {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Queued is the number of records not yet written.
func (s *Sender) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// EndVM ends a VM's run on this sender: it queues each open run of vm
// as an open run record, for wb-hostd to put in the VM's stop record,
// and forgets the VM's budget notice. wb-netd calls it before it exits,
// and wb-proxyd when the VM's hand-off socket ends.
func (s *Sender) EndVM(vm VM) {
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, r := range s.runs {
		if k.vm == vm {
			s.flushRunLocked(k, r, now, true)
		}
	}
	delete(s.budgets, vm)
}

// Close writes what is queued until the queue is empty or ctx ends,
// then stops the sender and closes the channel. It returns ctx's error
// when records were left, and logs how many.
func (s *Sender) Close(ctx context.Context) error {
	var err error
	for s.Queued() > 0 && err == nil {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(s.cfg.RetryEvery):
		}
	}
	if n := s.Queued(); n > 0 {
		s.log.Error("event records not written at close", "rule", string(RuleBlocked), "decision", "drop", "records", n)
	}
	s.once.Do(func() { close(s.quit) })
	closeErr := s.cfg.Conn.Close()
	s.wg.Wait()
	if err != nil {
		return err
	}
	return closeErr
}

// writeLoop writes the queue in order, the head again after a failure.
func (s *Sender) writeLoop() {
	defer s.wg.Done()
	for {
		rec, ok := s.next()
		if !ok {
			return
		}
		start := time.Now()
		err := s.cfg.Conn.Send(rec, nil)
		if s.sent(err, time.Since(start)) {
			continue
		}
		select {
		case <-s.quit:
			return
		case <-time.After(s.cfg.RetryEvery):
		}
	}
}

// next waits for a record to write and marks the write in flight. It
// returns false once the sender stops.
func (s *Sender) next() ([]byte, bool) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			rec := s.queue[0]
			s.sendingSince = time.Now()
			s.mu.Unlock()
			return rec, true
		}
		s.mu.Unlock()
		select {
		case <-s.work:
		case <-s.quit:
			return nil, false
		}
	}
}

// sent records the outcome of a write, and reports whether it
// succeeded. A failure, or a write that took longer than BlockAfter,
// blocks the channel. The head leaves the queue only on success, and an
// empty queue ends the block and the stall.
func (s *Sender) sent(err error, took time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendingSince = time.Time{}
	if took > BlockAfter {
		s.markBlockedLocked(fmt.Sprintf("a write took %s, more than %s", took.Round(time.Millisecond), BlockAfter))
	}
	if err != nil {
		s.markBlockedLocked("a write failed: " + err.Error())
		return false
	}
	s.queue[0] = nil
	s.queue = s.queue[1:]
	if len(s.queue) > 0 {
		return true
	}
	if s.blocked {
		s.blocked = false
		s.log.Info("event channel unblocked", "rule", string(RuleBlocked), "decision", "admit")
	}
	if s.stalled {
		s.stalled = false
		close(s.drained)
		s.log.Info("event queue drained, input resumed", "rule", string(RuleBlocked), "decision", "resume")
	}
	return true
}

func (s *Sender) markBlockedLocked(why string) {
	if s.blocked {
		return
	}
	s.blocked = true
	s.log.Warn("event channel blocked, new work refused", "rule", string(RuleBlocked), "decision", "refuse",
		"reason", why, "queued", len(s.queue))
}

// readLoop applies the budget notices wb-hostd sends, until the channel
// closes.
func (s *Sender) readLoop() {
	defer s.wg.Done()
	for {
		m, err := s.cfg.Conn.Recv()
		var ref *fdchan.Refusal
		switch {
		case errors.As(err, &ref) && ref.Rule == fdchan.RuleControlTruncated:
			// fdchan logged it and closed the channel: every later Recv
			// returns it again.
			s.log.Warn("event channel closed by fdchan, budget notices stop", "rule", string(ref.Rule))
			return
		case errors.As(err, &ref):
			// fdchan logged it, and the channel goes on.
			continue
		case errors.Is(err, io.EOF):
			s.log.Info("event channel closed by wb-hostd")
			return
		case err != nil:
			select {
			case <-s.quit:
			default:
				s.log.Warn("event channel read failed, budget notices stop", "error", err.Error())
			}
			return
		}
		s.onNotice(m)
	}
}

func (s *Sender) onNotice(m *fdchan.Message) {
	if m.File != nil {
		_ = m.Close()
		s.log.Warn("budget notice refused", "rule", string(RuleNoticeInvalid), "decision", "ignore",
			"reason", "a notice carries no descriptor")
		return
	}
	now := s.cfg.Now()
	n, ref := checkNotice(m.Record, s.cfg.Own, now)
	if ref != nil {
		s.log.Warn("budget notice refused", "rule", string(RuleNoticeInvalid), "decision", "ignore", "reason", ref.msg)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.budgets[n.vm]; ok && !n.until.After(cur.until) {
		return
	}
	s.budgets[n.vm] = refusal{rule: n.rule, until: n.until}
	s.log.Info("budget notice accepted, new work refused", "rule", string(n.rule), "decision", "refuse",
		"vm", n.vm.String(), "until", n.until.UTC().Format(time.RFC3339))
}

// tickLoop ends the run windows that have passed.
func (s *Sender) tickLoop(tick <-chan time.Time, stop func()) {
	defer s.wg.Done()
	defer stop()
	for {
		select {
		case <-tick:
			s.endWindows(s.cfg.Now())
		case <-s.quit:
			return
		}
	}
}
