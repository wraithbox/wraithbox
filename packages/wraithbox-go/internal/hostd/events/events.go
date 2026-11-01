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
package events

import (
	"errors"
	"io"
	"log/slog"
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
)

// maxOpenRuns bounds the open runs the hub holds for one VM's stop
// record. A run past it is written as its own record.
const maxOpenRuns = 64

// Log is the audit writer.
type Log interface {
	Record(r audit.Record)
	Budget(slot string) audit.Budget
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

	mu        sync.Mutex
	live      map[eventchan.VM]bool
	openRuns  map[eventchan.VM][]audit.OpenRun
	receivers map[*Receiver]bool
}

// NewHub returns a hub that writes to log.
func NewHub(log Log, logger *slog.Logger) *Hub {
	return &Hub{
		log: log, logger: logger,
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
	conn eventchan.Conn
	src  Source
	end  Ender
	last uint64
	done chan struct{}

	// sent is the last notice sent for each VM, guarded by hub.mu.
	sent map[eventchan.VM]audit.Budget
}

// Attach starts a receiver of src on conn. end ends the sender when a
// record is refused.
func (h *Hub) Attach(conn eventchan.Conn, src Source, end Ender) *Receiver {
	r := &Receiver{hub: h, conn: conn, src: src, end: end, done: make(chan struct{}), sent: map[eventchan.VM]audit.Budget{}}
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
		m, err := r.conn.Recv()
		if err != nil {
			r.failed(err)
			return
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
	r.hub.notify(vm, from.Slot)
	return true
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
// send that fails is logged and tried again with the next record.
func (h *Hub) notify(vm eventchan.VM, slot string) {
	b := h.log.Budget(slot)
	if b.Rule == "" {
		return
	}
	msg := &auditv1.BudgetNotice{Vm: &auditv1.VM{Slot: vm.Slot, Generation: vm.Generation}, UntilUnix: b.Until.Unix()}
	msg.Rule = auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE
	if b.Rule == audit.RuleBytes {
		msg.Rule = auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		h.logger.Error("budget notice not sent", "rule", b.Rule, "decision", "skip", "error", err.Error())
		return
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
}
