// Package events is wb-hostd's end of the event channel: it reads the
// records wb-netd and wb-proxyd send, checks them, writes them to the
// audit log, and tells the senders when a VM is over an audit budget
// (S04-08-records-and-bounds, "Event records"; S09-11-audit, "Budgets";
// SEC10-audit).
//
// Each sender has its own channel, an internal/platform/fdchan socket of
// records up to eventchan.MaxRecord bytes. A record that fdchan refuses,
// that doesn't decode or breaks a bound, that names a VM the sender may
// not name, that skips or repeats a sequence number, or that carries a
// descriptor is a crash of the sender: the receiver logs it with its
// rule, ends the sender through its Ender and reads no more. The guest
// can shape what a sender reports, so every record is treated as
// adversarial.
//
// A sender that ignores its budget notices is held back, never dropped
// (S04-08-records-and-bounds, "Host work the guest can cause";
// SEC13-bounded-resources). The receiver stops reading its channel while
// the audit writer holds its cap of records, and while the VM of the
// last record is past a budget by a margin. The sender's writes then
// fail, and it refuses new work with event-channel-blocked. Once the
// sender's stop begins, the receiver reads a bounded allowance past the
// holds, so the stop record gets the runs the sender flushes.
package events

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// The receiver's rules. A refusal of fdchan ends the sender with
// fdchan's own rule.
const (
	// RuleRecordInvalid: a record broke the contract of
	// proto/wraithbox/audit/v1, or carried a descriptor.
	RuleRecordInvalid = "event-record-invalid"
	// RuleChannelFailed: reading the channel failed with an error that is
	// neither a refusal nor the sender's exit.
	RuleChannelFailed = "event-channel-failed"
	// RuleQueueFull: the receiver stopped reading while the audit
	// writer's queue is at its cap.
	RuleQueueFull = "event-queue-full"
	// RuleBudgetIgnored: the receiver stopped reading while a VM is past
	// its budget by the margin, so its sender ignored the notice.
	RuleBudgetIgnored = "event-budget-ignored"
	// RuleStopAllowance: the receiver read past its holds for a sender
	// whose stop began, up to stopAllowance records.
	RuleStopAllowance = "event-stop-allowance"
)

// The margins past a VM's budget at which its sender counts as one that
// ignores the notice, and the receiver stops reading it
// (S04-08-records-and-bounds, "Host work the guest can cause"). A sender
// that heeds the notice still sends the records of work it admitted
// before, at most a full queue of them (eventchan.DefaultQueueCap), so
// the margins leave room for that.
const (
	eventsMargin = eventchan.DefaultQueueCap
	bytesMargin  = 2 * eventchan.DefaultQueueCap * eventchan.MaxRecord
)

// recheckEvery is how often a receiver held for a VM's budget checks
// the budget again.
const recheckEvery = time.Second

// maxOpenRuns bounds the open runs the hub holds for one VM's stop
// record. A run past it is written as its own record.
const maxOpenRuns = 64

// Log is the audit writer.
type Log interface {
	Record(r audit.Record)
	Budget(slot string) audit.Budget
	// Room is closed while the writer's queue is below its cap.
	Room() <-chan struct{}
}

// Ender ends a sender process: wb-hostd's restart rules decide what
// follows (M3.11c, #480).
type Ender interface {
	End(rule string)
}

// Source is a sender: wb-netd of one VM, or wb-proxyd, which serves both.
type Source struct {
	Sender string
	// VM is wb-netd's VM, and nil for wb-proxyd.
	VM *eventchan.VM
}

// Netd is the source of the wb-netd of vm.
func Netd(vm eventchan.VM) Source { return Source{Sender: "wb-netd", VM: &vm} }

// Proxyd is the source of wb-proxyd.
func Proxyd() Source { return Source{Sender: "wb-proxyd"} }

// serves reports whether src takes notices for vm.
func (s Source) serves(vm eventchan.VM) bool { return s.VM == nil || *s.VM == vm }

// Hub holds the VMs that run and the receivers of their senders.
type Hub struct {
	log    Log
	logger *slog.Logger
	// recheck is recheckEvery, shorter in tests, and allowance is
	// stopAllowance, smaller in tests.
	recheck   time.Duration
	allowance int

	mu        sync.Mutex
	live      map[eventchan.VM]bool
	openRuns  map[eventchan.VM][]audit.OpenRun
	receivers map[*Receiver]bool
}

// NewHub returns a hub that writes to log.
func NewHub(log Log, logger *slog.Logger) *Hub {
	return &Hub{
		log: log, logger: logger, recheck: recheckEvery, allowance: stopAllowance,
		live: map[eventchan.VM]bool{}, openRuns: map[eventchan.VM][]audit.OpenRun{}, receivers: map[*Receiver]bool{},
	}
}

// Begin marks vm as running, so wb-proxyd may name it.
func (h *Hub) Begin(vm eventchan.VM) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.live[vm] = true
}

// End marks vm as stopped and returns the runs its senders sent as
// open, for its stop record (S09-11-audit, "Runs with a count"). It
// forgets the notices sent for vm. The caller first waits for the VM's
// wb-netd receiver to be Done, and for wb-proxyd's open runs of the VM.
// An open run that arrives after End is written as its own record.
func (h *Hub) End(vm eventchan.VM) []audit.OpenRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.live, vm)
	runs := h.openRuns[vm]
	delete(h.openRuns, vm)
	for r := range h.receivers {
		delete(r.sent, vm)
	}
	return runs
}

func (h *Hub) isLive(vm eventchan.VM) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live[vm]
}

// Receiver reads one sender's channel.
type Receiver struct {
	hub  *Hub
	conn Conn
	src  Source
	end  Ender
	last uint64
	done chan struct{}
	// gone is closed once the sender has exited, see SenderGone, and
	// stopping once its stop began, see Stopping.
	gone     chan struct{}
	goneOnce sync.Once
	stopping chan struct{}
	stopOnce sync.Once
	// spent is the records read since the sender's stop began, against
	// stopAllowance. The loop's own, as are the hold logs.
	spent               int
	queueLog, budgetLog holdLog
	// over is the VM of the last record when it was past a budget by
	// the margin, read by the next hold. The loop's own.
	over *eventchan.VM

	// sent is the last notice sent for each VM, guarded by hub.mu.
	sent map[eventchan.VM]audit.Budget
}

// Attach starts a receiver of src on conn. end ends the sender when a
// record is refused.
func (h *Hub) Attach(conn Conn, src Source, end Ender) *Receiver {
	r := &Receiver{
		hub: h, conn: conn, src: src, end: end, done: make(chan struct{}),
		gone: make(chan struct{}), stopping: make(chan struct{}),
		sent: map[eventchan.VM]audit.Budget{},
	}
	h.mu.Lock()
	h.receivers[r] = true
	h.mu.Unlock()
	go r.loop()
	return r
}

// Done is closed once the receiver stops: the sender closed its end, or
// a record ended it.
func (r *Receiver) Done() <-chan struct{} { return r.done }

func (r *Receiver) loop() {
	defer close(r.done)
	defer r.detach()
	for {
		r.holdBudget()
		r.holdQueue()
		if r.isGone() {
			r.drain()
			return
		}
		m, err := r.conn.Recv()
		if errors.Is(err, os.ErrDeadlineExceeded) && r.isGone() {
			r.drain()
			return
		}
		if err != nil {
			r.failed(err)
			return
		}
		if r.isStopping() {
			r.spent++
		}
		if !r.accept(m) {
			return
		}
	}
}

func (r *Receiver) detach() {
	_ = r.conn.Close()
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	delete(r.hub.receivers, r)
}

// failed handles an error of Recv: the sender's exit, or a refusal that
// ends it.
func (r *Receiver) failed(err error) {
	var ref *fdchan.Refusal
	switch {
	case errors.Is(err, io.EOF):
		r.hub.logger.Info("event channel closed by its sender", "sender", r.src.Sender, "records", r.last)
	case errors.As(err, &ref):
		r.endSender(string(ref.Rule), err.Error())
	default:
		r.endSender(RuleChannelFailed, err.Error())
	}
}

func (r *Receiver) endSender(rule, reason string) {
	r.hub.logger.Error("event sender ended", "rule", rule, "decision", "end-sender",
		"sender", r.src.Sender, "after_sequence", r.last, "reason", reason)
	r.end.End(rule)
}

// accept checks one message and writes its record. It reports whether
// the receiver reads on.
func (r *Receiver) accept(m *fdchan.Message) bool {
	if m.File != nil {
		_ = m.Close()
		r.endSender(RuleRecordInvalid, "a record carries a descriptor")
		return false
	}
	rec, vm, bad := check(m.Record, r.src, r.last, r.hub.isLive)
	if bad != nil {
		r.endSender(RuleRecordInvalid, bad.reason)
		return false
	}
	r.last = rec.GetSequence()
	from := audit.From{
		Slot: eventchan.SlotName(vm.Slot), Generation: vm.Generation, Sender: r.src.Sender, Sequence: rec.GetSequence(),
	}
	if run := rec.GetRun(); run != nil {
		r.writeRun(from, vm, run)
	} else {
		r.hub.log.Record(audit.EventFromChannel(from, channelEvent(rec.GetEvent())))
	}
	if b := r.hub.notify(vm, from.Slot); pastMargin(b) {
		r.over = &vm
	}
	return true
}

// pastMargin reports whether a VM is past one of its budgets by the
// margin.
func pastMargin(b audit.Budget) bool {
	return b.EventsThisMinute >= b.EventsPerMinute+eventsMargin || b.BytesToday >= b.BytesPerDay+bytesMargin
}

// writeRun writes a run, or holds an open one for the VM's stop record.
func (r *Receiver) writeRun(from audit.From, vm eventchan.VM, run *auditv1.Run) {
	if run.GetOpen() {
		h := r.hub
		h.mu.Lock()
		held := h.live[vm] && len(h.openRuns[vm]) < maxOpenRuns
		if held {
			h.openRuns[vm] = append(h.openRuns[vm], audit.OpenRun{
				Sender: from.Sender, ClassUID: int(run.GetClass()), Decision: run.GetDecision(), Rule: run.GetRule(),
				Count: run.GetCount(), WindowSeconds: run.GetWindowSeconds(),
			})
		}
		h.mu.Unlock()
		if held {
			return
		}
	}
	r.hub.log.Record(audit.RunFromChannel(from, audit.ChannelRun{
		ClassUID: int(run.GetClass()), Decision: run.GetDecision(), Rule: run.GetRule(),
		Count: run.GetCount(), WindowSeconds: run.GetWindowSeconds(), Open: run.GetOpen(),
	}))
}

func channelEvent(e *auditv1.Event) audit.ChannelEvent {
	l := e.GetGuestLabel()
	return audit.ChannelEvent{
		ClassUID: int(e.GetClass()), Decision: e.GetDecision(), Rule: e.GetRule(),
		Host: e.GetDestinationHost(), Port: e.GetDestinationPort(),
		Method: e.GetHttpMethod(), Path: e.GetHttpPath(), BytesIn: e.GetBytesIn(), BytesOut: e.GetBytesOut(),
		Label: audit.GuestLabel{Project: l.GetProject(), Session: l.GetSession(), User: l.GetUser(), Program: l.GetProgram()},
	}
}

// notify sends each sender of vm a BudgetNotice when vm is over a budget
// and the sender hasn't had this one yet (S09-11-audit, "Budgets"). A
// send that fails is logged and tried again with the next record. It
// returns the VM's budget.
func (h *Hub) notify(vm eventchan.VM, slot string) audit.Budget {
	b := h.log.Budget(slot)
	if b.Rule == "" {
		return b
	}
	msg := &auditv1.BudgetNotice{Vm: &auditv1.VM{Slot: vm.Slot, Generation: vm.Generation}, UntilUnix: b.Until.Unix()}
	msg.Rule = auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE
	if b.Rule == audit.RuleBytes {
		msg.Rule = auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		h.logger.Error("budget notice not sent", "rule", b.Rule, "decision", "skip", "error", err.Error())
		return b
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for r := range h.receivers {
		last := r.sent[vm]
		if !r.src.serves(vm) || last.Rule == b.Rule && last.Until.Equal(b.Until) {
			continue
		}
		if err := r.conn.Send(data, nil); err != nil {
			h.logger.Warn("budget notice not sent, retried with the next record", "rule", b.Rule, "decision", "retry",
				"sender", r.src.Sender, "vm", vm.String(), "error", err.Error())
			continue
		}
		r.sent[vm] = b
		h.logger.Info("budget notice sent", "rule", b.Rule, "decision", "refuse-new-work",
			"sender", r.src.Sender, "vm", vm.String(), "until", b.Until.UTC().Format(time.RFC3339),
			"events_this_minute", b.EventsThisMinute, "bytes_today", b.BytesToday)
	}
	return b
}
