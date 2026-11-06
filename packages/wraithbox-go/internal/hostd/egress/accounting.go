package egress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// Reader reads one sender's accounting channel into a State.
type Reader struct {
	s    *State
	conn Conn
	src  Source
	end  Ender
	own  string
	read uint64
	done chan struct{}
}

// Read sends wb-hostd's version record on conn, the accounting channel
// of src, and reads it until the sender closes it or a record ends the
// sender through end. own is wb-hostd's build version.
//
// wb-hostd reads what is left in the channel after the sender exits,
// before it starts a new one (S04-08-records-and-bounds, "Accounting
// records"): wait for Done.
func (s *State) Read(conn Conn, src Source, end Ender, own string) *Reader {
	r := &Reader{s: s, conn: conn, src: src, end: end, own: own, done: make(chan struct{})}
	go r.loop()
	return r
}

// Done is closed once the reader stops.
func (r *Reader) Done() <-chan struct{} { return r.done }

func (r *Reader) loop() {
	defer close(r.done)
	defer func() { _ = r.conn.Close() }()
	if !r.versions() {
		return
	}
	for {
		m, err := r.conn.Recv()
		if err != nil {
			r.failed(err)
			return
		}
		ref := r.s.Record(r.src, m.Record, m.File != nil)
		_ = m.Close()
		if ref != nil {
			r.refused(ref)
			return
		}
		r.read++
	}
}

// versions sends wb-hostd's version record and checks that the sender's
// is the first record, within hostversion.Timeout (S04-architecture,
// "The version record"). hostversion logs its own refusals.
func (r *Reader) versions() bool {
	if err := r.conn.Send(hostversion.Record(r.own), nil); err != nil {
		r.endSender(RuleAccountingFailed, "send-version", err.Error())
		return false
	}
	if err := r.conn.SetReadDeadline(time.Now().Add(hostversion.Timeout)); err != nil {
		r.endSender(RuleAccountingFailed, "deadline", err.Error())
		return false
	}
	m, err := r.conn.Recv()
	if errors.Is(err, os.ErrDeadlineExceeded) {
		ref := hostversion.RecordTimeout(r.s.log, r.own, err)
		r.endSender(hostversion.RuleTimeout, "version-record", ref.Error())
		return false
	}
	if err != nil {
		r.failed(err)
		return false
	}
	hasFD := m.File != nil
	_ = m.Close()
	if hasFD {
		r.endSender(RuleRecordMalformed, CheckDescriptor, "the version record carries a descriptor")
		return false
	}
	if _, err := hostversion.CheckRecord(r.s.log, r.own, m.Record); err != nil {
		rule := hostversion.RuleMalformed
		if hr, ok := errors.AsType[*hostversion.Refusal](err); ok {
			rule = hr.Rule
		}
		r.endSender(rule, "version-record", err.Error())
		return false
	}
	if err := r.conn.SetReadDeadline(time.Time{}); err != nil {
		r.endSender(RuleAccountingFailed, "deadline", err.Error())
		return false
	}
	return true
}

// failed handles an error of Recv: the sender's exit, or a refusal of
// the channel, such as an oversized record or one received with
// MSG_TRUNC, which ends the sender (S04-08-records-and-bounds).
func (r *Reader) failed(err error) {
	if ref, ok := errors.AsType[*fdchan.Refusal](err); ok {
		r.endSender(RuleRecordMalformed, string(ref.Rule), err.Error())
		return
	}
	if errors.Is(err, io.EOF) {
		r.s.log.Info("accounting channel closed by its sender",
			"sender", r.src.String(), "vm", r.src.VM, "records", r.read)
		return
	}
	r.endSender(RuleAccountingFailed, "recv", err.Error())
}

// refused logs a refused record and ends its sender. A refused mapping
// entry's log record is rate-limited (S04-08-records-and-bounds,
// "Mapping entries are checked"). The sender is ended either way.
func (r *Reader) refused(ref *Refused) {
	log := r.s.log
	if ref.Rule == RuleEntryInvalid {
		log = r.s.rate
	}
	r.logEnd(log, ref.Rule, ref.Check, ref.Reason, slog.String("kind", ref.Kind.String()))
	r.end.End(ref.Rule)
}

func (r *Reader) endSender(rule, check, reason string) {
	r.logEnd(r.s.log, rule, check, reason)
	r.end.End(rule)
}

func (r *Reader) logEnd(log *slog.Logger, rule, check, reason string, extra ...slog.Attr) {
	attrs := append([]slog.Attr{
		slog.String("rule", rule), slog.String("decision", "end-sender"), slog.String("check", check),
		slog.String("sender", r.src.String()), slog.String("vm", r.src.VM),
		slog.Uint64("after_records", r.read), slog.String("reason", reason),
	}, extra...)
	log.LogAttrs(context.Background(), slog.LevelError, "accounting sender ended", attrs...)
}
