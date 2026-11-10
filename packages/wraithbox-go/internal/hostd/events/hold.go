package events

import (
	"errors"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// The holds of S04-08-records-and-bounds, "Host work the guest can
// cause" (SEC13-bounded-resources): a sender that ignores its budget is
// held back, and the stop of its VM still gets every record it sent.

// stopAllowance is the records a receiver reads past its holds once its
// sender's stop began: one sender's queue (eventchan.DefaultQueueCap)
// and as many again for its socket's buffer. A stop that waits on a
// held receiver would see the sender drop the records it flushes at
// its close, its open runs among them, so the receiver reads them. A
// sender that ignores the stop can't send past the allowance: the holds
// apply again, and once it is gone the drain reads no more than what is
// left of the allowance.
const stopAllowance = 2 * eventchan.DefaultQueueCap

// holdLogEvery is how often a receiver logs a hold that repeats.
const holdLogEvery = time.Second

// Conn is the receiver's end of an event channel. fdchan.Channel
// implements it. TryRecv returns fdchan.ErrNoMessage when none is queued.
type Conn interface {
	eventchan.Conn
	TryRecv() (*fdchan.Message, error)
	SetReadDeadline(t time.Time) error
}

// holdLog limits the log lines of one hold's rule: the first hold, then
// at most one a second, with the holds since the last line. The loop's
// own.
type holdLog struct {
	last  time.Time
	holds int
}

// due counts a hold and reports whether to log it, with the holds the
// line stands for.
func (l *holdLog) due(now time.Time) (int, bool) {
	l.holds++
	if !l.last.IsZero() && now.Sub(l.last) < holdLogEvery {
		return 0, false
	}
	n := l.holds
	l.last, l.holds = now, 0
	return n, true
}

// Stopping tells the receiver that its sender's stop began: the sender
// may flush what it holds, such as its open runs, and wait for it to be
// read before it exits. The receiver reads up to stopAllowance records
// past its holds, so the stop gets them. Call it before the sender is
// told to stop.
func (r *Receiver) Stopping() { r.stopOnce.Do(func() { close(r.stopping) }) }

// SenderGone tells the receiver that its sender has exited. The
// receiver reads what the sender left, without holding back, until the
// channel is empty or what is left of stopAllowance is read, and stops.
// The caller that waits for Done, such as a VM stop, doesn't wait for a
// budget's turn or for a writer whose writes fail. Call it before
// waiting for Done.
func (r *Receiver) SenderGone() {
	r.goneOnce.Do(func() {
		close(r.gone)
		// Wake a Recv that waits.
		_ = r.conn.SetReadDeadline(time.Now())
	})
}

func (r *Receiver) isGone() bool {
	select {
	case <-r.gone:
		return true
	default:
		return false
	}
}

func (r *Receiver) isStopping() bool {
	select {
	case <-r.stopping:
		return true
	default:
		return false
	}
}

// released reports whether the holds let the next read through: the
// sender is gone, or its stop began and the allowance isn't spent.
func (r *Receiver) released() bool {
	return r.isGone() || r.isStopping() && r.spent < r.hub.allowance
}

// stopWake is a channel that wakes a hold when the sender's stop
// begins, nil once the allowance is spent so a hold doesn't spin on it.
func (r *Receiver) stopWake() <-chan struct{} {
	if r.spent >= r.hub.allowance {
		return nil
	}
	return r.stopping
}

// holdBudget waits before the next read while the VM of the last record
// is past a budget by the margin, until the budget's minute or day turns
// (S04-08-records-and-bounds, "Host work the guest can cause";
// SEC13-bounded-resources). It stops waiting once the holds are
// released, or for wb-proxyd once the VM stops.
func (r *Receiver) holdBudget() {
	if r.over == nil {
		return
	}
	vm := *r.over
	r.over = nil
	slot := eventchan.SlotName(vm.Slot)
	b := r.hub.log.Budget(slot)
	if r.released() || !pastMargin(b) {
		return
	}
	if n, ok := r.budgetLog.due(time.Now()); ok {
		r.hub.logger.Warn("event channel held, the sender ignores its budget", "rule", RuleBudgetIgnored, "decision", "hold",
			"sender", r.src.Sender, "vm", vm.String(), "holds", n, "events_this_minute", b.EventsThisMinute,
			"bytes_today", b.BytesToday, "until", b.Until.UTC().Format(time.RFC3339))
	}
	t := time.NewTicker(r.hub.recheck)
	defer t.Stop()
	for pastMargin(b) && (r.src.VM != nil || r.hub.isLive(vm)) {
		select {
		case <-t.C:
		case <-r.gone:
			return
		case <-r.stopWake():
			return
		}
		b = r.hub.log.Budget(slot)
	}
}

// holdQueue waits before the next read while the audit writer holds its
// cap of records (S04-08-records-and-bounds, "Host work the guest can
// cause"; SEC13-bounded-resources). Each receiver adds at most one
// record before it waits again, so the writer holds at most the cap
// plus one record per receiver, and the stop allowance of the senders
// that stop. It stops waiting once the holds are released.
func (r *Receiver) holdQueue() {
	room := r.hub.log.Room()
	select {
	case <-room:
		return
	default:
	}
	if r.released() {
		return
	}
	if n, ok := r.queueLog.due(time.Now()); ok {
		r.hub.logger.Warn("event channel held, the audit queue is full", "rule", RuleQueueFull, "decision", "hold",
			"sender", r.src.Sender, "holds", n, "after_sequence", r.last)
	}
	select {
	case <-room:
	case <-r.gone:
	case <-r.stopWake():
	}
}

// drain reads what a gone sender left, until the channel is empty, the
// stop allowance is spent, or a record ends the receiver. It logs the
// records it read, and the rule when the allowance cut it short.
func (r *Receiver) drain() {
	read := 0
	defer func() {
		r.hub.logger.Info("event channel drained, the sender is gone", "rule", RuleStopAllowance, "decision", "drain",
			"sender", r.src.Sender, "records", read, "after_sequence", r.last)
	}()
	for r.spent < r.hub.allowance {
		m, err := r.conn.TryRecv()
		if errors.Is(err, fdchan.ErrNoMessage) {
			return
		}
		if err != nil {
			r.failed(err)
			return
		}
		r.spent++
		read++
		if !r.accept(m) {
			return
		}
	}
	m, err := r.conn.TryRecv()
	if err != nil {
		return
	}
	_ = m.Close()
	r.hub.logger.Warn("event records left unread, the sender sent past its stop allowance", "rule", RuleStopAllowance,
		"decision", "drop", "sender", r.src.Sender, "allowance", r.hub.allowance, "after_sequence", r.last)
}
