package handoff

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"syscall"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// logCut is how many bytes of a name from wb-netd a log record holds,
// the longest name of the name form (S07-03-dns, "Name form").
const logCut = 253

// socket is one hand-off socket of a VM and generation. One goroutine
// reads it and another decides its messages in the order they were read
// (S07-05-stream-hand-off, "Message form"). Between them a queue of
// Caps.Pending messages bounds what is read and not yet decided
// ("Caps"). Each socket has its own goroutines, so one VM's flood
// doesn't hold up another VM's streams.
type socket struct {
	r   *Receiver
	vm  *vmState
	gen uint64
	ch  Conn

	queue      chan item
	done       chan struct{}
	readerDone chan struct{}

	// closed and streams are guarded by the receiver's lock.
	closed  bool
	streams map[*Stream]struct{}
}

// item is one result of a read: a message or the error that ended the
// reads.
type item struct {
	msg *fdchan.Message
	err error
}

func newSocket(r *Receiver, st *vmState, gen uint64, ch Conn) *socket {
	return &socket{
		r: r, vm: st, gen: gen, ch: ch,
		queue:      make(chan item, r.cfg.Caps.Pending),
		done:       make(chan struct{}),
		readerDone: make(chan struct{}),
		streams:    map[*Stream]struct{}{},
	}
}

// run starts the socket's reader and decider. It counts them under the
// receiver's lock, so Close can't wait on the group while they are
// added, and it starts nothing on a socket or receiver that already
// closed. It returns false then.
func (s *socket) run() bool {
	s.r.mu.Lock()
	if s.r.closed || s.closed {
		s.r.mu.Unlock()
		return false
	}
	s.r.wg.Add(2)
	s.r.mu.Unlock()
	go s.read()
	go s.decide()
	return true
}

// read reads messages into the queue until a read fails or the socket
// closes. A full queue stops it, so further messages wait in the
// kernel's buffer and wb-netd's sends fail (S07-05-stream-hand-off,
// "Caps" and "Transport"; SEC13-bounded-resources).
func (s *socket) read() {
	defer s.r.wg.Done()
	defer close(s.readerDone)
	var dropped *fdchan.Refusal
	for {
		m, err := s.ch.Recv()
		if ref := s.droppedMessage(err, dropped); ref != nil {
			dropped = ref
			continue
		}
		select {
		case s.queue <- item{msg: m, err: err}:
		case <-s.done:
			if m != nil {
				_ = m.Close()
			}
			return
		}
		if err != nil {
			return
		}
	}
}

// decide decides the queued messages in order. The first must be
// wb-netd's version record.
func (s *socket) decide() {
	defer s.r.wg.Done()
	first := true
	for {
		it, ok := s.next()
		if !ok {
			break
		}
		if !s.handle(it, first) {
			s.close()
			break
		}
		first = false
	}
	<-s.readerDone
	s.drain()
}

// next returns the next queued item, or false once the socket closed.
func (s *socket) next() (item, bool) {
	select {
	case <-s.done:
		return item{}, false
	case it := <-s.queue:
		return it, true
	}
}

// drain closes the descriptors of the messages left in the queue.
func (s *socket) drain() {
	for {
		select {
		case it := <-s.queue:
			if it.msg != nil {
				_ = it.msg.Close()
			}
		default:
			return
		}
	}
}

// droppedMessage returns the refusal when err is fdchan dropping one
// message because the descriptor table was full, and nil otherwise. fdchan closed what arrived
// with it, and it says nothing of wb-netd, so the socket carries on.
// When fdchan couldn't drop the message it ended the channel, and
// every later Recv returns the same refusal again. A refusal that comes
// twice is that end, and so is one after the socket closed: the read
// then ends, so the reader never spins.
func (s *socket) droppedMessage(err error, prev *fdchan.Refusal) *fdchan.Refusal {
	ref, ok := errors.AsType[*fdchan.Refusal](err)
	if !ok || ref.Rule != fdchan.RuleFDTableFull || ref == prev {
		return nil
	}
	select {
	case <-s.done:
		return nil
	default:
	}
	s.r.log.LogAttrs(context.Background(), slog.LevelWarn, "hand-off message dropped",
		slog.String("rule", string(ref.Rule)), slog.String("vm", s.vm.slot), slog.Uint64("generation", s.gen))
	return ref
}

// handle decides one item, and returns false when the socket must
// close.
func (s *socket) handle(it item, first bool) bool {
	if it.err != nil {
		return s.readFailed(it.err)
	}
	m := it.msg
	if first {
		return s.versionRecord(m)
	}
	msg, ref := egressmsg.DecodeNetdMessage(m.Record, m.File != nil)
	if ref != nil {
		_ = m.Close()
		s.malformed(ref.Check, ref.Check, refusalAttrs(ref)...)
		return false
	}
	switch msg.Kind {
	case egressmsg.KindNameReport:
		s.report(msg.Name, msg.Addr)
	case egressmsg.KindRelease:
		s.release(msg.Addr)
	case egressmsg.KindStream:
		s.stream(msg, m.File)
	}
	return true
}

// readFailed handles the error that ended the reads. An fdchan refusal
// is a malformed message (S07-05-stream-hand-off, "Message form"),
// except a full descriptor table, which is this process's. End of file,
// or any other error, ends the socket.
func (s *socket) readFailed(err error) bool {
	if ref, ok := errors.AsType[*fdchan.Refusal](err); ok && ref.Rule != fdchan.RuleFDTableFull {
		s.malformed(egressmsg.CheckMessageForm, egressmsg.CheckMessageForm, slog.String("fdchan_rule", string(ref.Rule)))
		return false
	}
	level := slog.LevelInfo
	if !isEOF(err) {
		level = slog.LevelWarn
	}
	s.r.log.LogAttrs(context.Background(), level, "hand-off socket ended",
		slog.String("rule", RuleHandoffSocketEnded), slog.String("vm", s.vm.slot),
		slog.Uint64("generation", s.gen), slog.String("error", err.Error()))
	return false
}

func isEOF(err error) bool { return errors.Is(err, io.EOF) }

// versionRecord checks that the first record is wb-netd's version
// record of this build, with no descriptor. A refusal is malformed,
// reported to wb-hostd as message-form.
func (s *socket) versionRecord(m *fdchan.Message) bool {
	if m.File != nil {
		_ = m.Close()
		s.malformed(CheckVersion, egressmsg.CheckMessageForm, slog.String("reason", "the version record carries a descriptor"))
		return false
	}
	if _, err := hostversion.CheckRecord(s.r.cfg.Log, s.r.cfg.Version, m.Record); err != nil {
		s.malformed(CheckVersion, egressmsg.CheckMessageForm, slog.String("reason", err.Error()))
		return false
	}
	return true
}

// malformed reports a malformed message from wb-netd: it logs it with
// name-report-malformed and the check, and tells wb-hostd with a
// HandoffSocketClosed that names the VM, the generation and the check
// it takes (S07-06-name-mapping, "Malformed reports"). The caller then
// closes the socket and its streams.
func (s *socket) malformed(check, reported egressmsg.Check, attrs ...slog.Attr) {
	if !closedCheck(reported) {
		reported = egressmsg.CheckMessageForm
	}
	s.r.refused(Event{VM: s.vm.slot, Generation: s.gen, Rule: RuleNameReportMalformed, Check: check},
		append(attrs, slog.String("reported_check", string(reported)))...)
	err := s.r.sendHostd(&egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_HandoffSocketClosed{
		HandoffSocketClosed: &egressv1.HandoffSocketClosed{
			Vm: s.vm.slot, Generation: s.gen, Rule: RuleNameReportMalformed, Check: string(reported),
		},
	}})
	if err != nil {
		s.r.log.LogAttrs(context.Background(), slog.LevelError, "HandoffSocketClosed not sent",
			slog.String("rule", RuleNameReportMalformed), slog.String("vm", s.vm.slot),
			slog.Uint64("generation", s.gen), slog.String("error", err.Error()))
	}
}

// closedCheck reports whether a HandoffSocketClosed may name c.
func closedCheck(c egressmsg.Check) bool {
	return c == egressmsg.CheckMessageForm || c == egressmsg.CheckNameForm || c == egressmsg.CheckSyntheticRange
}

// report decides a name report (S07-06-name-mapping, "Name reports").
// A name that adds to the second wildcard count is written to wb-hostd
// first, outside the lock, and then decided again: the policy or the
// copy may have changed during the write.
func (s *socket) report(name dnsname.Name, addr netip.Addr) {
	o := s.decideReport(name, addr, false)
	if o.action == reportAddWildcard {
		if check := s.writeWildcard(name); check != "" {
			o = reportOutcome{action: reportRefuse, check: check, live: true}
		} else {
			o = s.decideReport(name, addr, true)
		}
	}
	if !o.live {
		return
	}
	if o.check != "" {
		s.r.refused(Event{VM: s.vm.slot, Generation: s.gen, Rule: RuleNameReportRefused, Check: o.check},
			slog.String("name", guesttext.LogField(name.String(), logCut)), slog.String("address", addr.String()))
	}
	for _, st := range o.conflicted {
		if st.Reset() {
			s.r.refused(Event{VM: s.vm.slot, Generation: s.gen, Rule: RuleAddressConflict, Check: CheckAddressConflict},
				streamAttrs(st)...)
		}
	}
}

// reportOutcome is what decideReport did.
type reportOutcome struct {
	action reportAction
	check  egressmsg.Check
	// conflicted are the open streams to an address a conflict removed.
	conflicted []*Stream
	// live is false when the socket had closed, and the report was
	// dropped unread.
	live bool
}

// decideReport decides a report under the lock and applies it. A name
// that adds to the wildcard count is applied only when written is true.
func (s *socket) decideReport(name dnsname.Name, addr netip.Addr, written bool) reportOutcome {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.closed {
		return reportOutcome{}
	}
	a, check := s.vm.copy.decideReport(s.vm.policy, name, addr)
	o := reportOutcome{action: a, check: check, live: true}
	switch a {
	case reportAddWildcard:
		if written {
			s.vm.copy.apply(a, name, addr)
		}
	case reportConflict:
		s.vm.copy.apply(a, name, addr)
		for st := range s.streams {
			if st.Addr == addr {
				o.conflicted = append(o.conflicted, st)
			}
		}
	case reportAdd, reportRepeat, reportRefuse:
		s.vm.copy.apply(a, name, addr)
	}
	return o
}

// writeWildcard writes the ProxydWildcardName record. A write that fails
// or takes longer than WildcardWriteTimeout fails the report with
// wildcard-budget (S07-06-name-mapping, "Wildcard budget, second
// layer").
func (s *socket) writeWildcard(name dnsname.Name) egressmsg.Check {
	ctx, cancel := context.WithTimeout(context.Background(), WildcardWriteTimeout)
	defer cancel()
	began := time.Now()
	err := s.r.cfg.Wildcard.RecordWildcardName(ctx, s.vm.slot, name)
	took := time.Since(began)
	if err == nil && took <= WildcardWriteTimeout {
		return ""
	}
	reason := "the write took longer than the timeout"
	if err != nil {
		reason = err.Error()
	}
	s.r.log.LogAttrs(context.Background(), slog.LevelWarn, "wildcard name write failed",
		slog.String("rule", RuleNameReportRefused), slog.String("check", string(CheckWildcardBudget)),
		slog.String("vm", s.vm.slot), slog.Duration("took", took), slog.String("reason", reason))
	return CheckWildcardBudget
}

// release removes an entry (S07-06-name-mapping, "Releases"). Open
// streams to the address keep the name they got. A release for an
// address the copy doesn't hold is logged and ignored.
func (s *socket) release(addr netip.Addr) {
	s.r.mu.Lock()
	if s.closed {
		s.r.mu.Unlock()
		return
	}
	held := s.vm.copy.release(addr)
	s.r.mu.Unlock()
	if !held {
		s.r.log.LogAttrs(context.Background(), slog.LevelInfo, "release ignored",
			slog.String("rule", RuleReleaseUnheld), slog.String("decision", "ignore"),
			slog.String("vm", s.vm.slot), slog.Uint64("generation", s.gen), slog.String("address", addr.String()))
	}
}

// stream checks a stream (S07-05-stream-hand-off, "Streams";
// SEC08-proj-isolation, SEC05-default-deny). Its VM is the socket's and
// its name the copy's. One that passes goes to the handler. One that
// doesn't is reset before anything resolves its name, connects upstream
// or fetches a binding.
func (s *socket) stream(msg egressmsg.NetdMessage, f *os.File) {
	conn, isStream := streamConn(f)
	s.r.mu.Lock()
	if s.closed {
		s.r.mu.Unlock()
		closeEither(conn, f)
		return
	}
	name, check := decideStream(s.vm.copy, s.vm.policy, msg, streamFacts{
		socketType: isStream, generation: s.gen, open: len(s.streams), maxStreams: s.r.cfg.Caps.Streams,
	})
	if check != "" {
		s.r.mu.Unlock()
		s.r.refused(Event{VM: s.vm.slot, Generation: s.gen, Rule: RuleStreamHandoffRefused, Check: check},
			slog.Uint64("stream_id", msg.StreamID), slog.Uint64("stream_generation", msg.Generation),
			slog.String("address", msg.Addr.String()), slog.Int("port", int(msg.Port)))
		// Resets: the StreamReset goes before the descriptor closes.
		s.sendReset(msg.StreamID)
		closeEither(conn, f)
		return
	}
	st := &Stream{
		VM: s.vm.slot, Generation: s.gen, ID: msg.StreamID,
		Name: name, Addr: msg.Addr, Port: msg.Port, Conn: conn, sock: s,
		policy: s.vm.policy,
	}
	s.streams[st] = struct{}{}
	s.r.mu.Unlock()
	s.r.cfg.Handler.HandleStream(st)
}

// streamConn returns the descriptor as a connection when it is an
// AF_UNIX SOCK_STREAM socket, and then closes f. Otherwise it returns
// false and leaves f open.
func streamConn(f *os.File) (net.Conn, bool) {
	family, sockType, err := fdchan.SocketType(f)
	if err != nil || family != syscall.AF_UNIX || sockType != syscall.SOCK_STREAM {
		return nil, false
	}
	c, err := net.FileConn(f)
	if err != nil {
		return nil, false
	}
	_ = f.Close()
	return c, true
}

// closeEither closes the connection when there is one, and f otherwise.
func closeEither(c net.Conn, f *os.File) {
	if c != nil {
		_ = c.Close()
		return
	}
	_ = f.Close()
}

// sendReset sends a StreamReset in one attempt. When it fails the
// hand-off socket closes, which wb-netd reads as a reset of every
// stream of the VM (S07-05-stream-hand-off, "Resets").
func (s *socket) sendReset(id uint64) {
	b, err := egressmsg.Marshal(&egressv1.ProxydMessage{Kind: &egressv1.ProxydMessage_StreamReset{
		StreamReset: &egressv1.StreamReset{StreamId: id},
	}})
	if err == nil {
		err = s.ch.Send(b, nil)
	}
	if err == nil {
		return
	}
	s.r.log.LogAttrs(context.Background(), slog.LevelWarn, "StreamReset not sent, hand-off socket closed",
		slog.String("rule", RuleStreamResetFailed), slog.String("vm", s.vm.slot),
		slog.Uint64("generation", s.gen), slog.Uint64("stream_id", id), slog.String("error", err.Error()))
	s.close()
}

// openStreams returns the streams open from the socket. The caller
// holds the lock.
func (s *socket) openStreams() []*Stream {
	out := make([]*Stream, 0, len(s.streams))
	for st := range s.streams {
		out = append(out, st)
	}
	return out
}

// remove takes an ended stream out of the socket's count.
func (s *socket) remove(st *Stream) {
	s.r.mu.Lock()
	delete(s.streams, st)
	s.r.mu.Unlock()
}

// close closes the socket and every stream that came on it
// (S07-05-stream-hand-off, "One hand-off socket per VM"). It is safe
// to call more than once.
func (s *socket) close() {
	s.r.mu.Lock()
	if s.closed {
		s.r.mu.Unlock()
		return
	}
	s.closed = true
	open := s.openStreams()
	if s.vm.sock == s {
		s.vm.sock = nil
	}
	s.r.mu.Unlock()
	close(s.done)
	_ = s.ch.Close()
	for _, st := range open {
		st.end(false)
	}
}

// canonicalNames checks names as egressmsg does and returns them.
func canonicalNames(list []string) ([]dnsname.Name, *egressmsg.Refusal) {
	out := make([]dnsname.Name, 0, len(list))
	for _, n := range list {
		name, ref := egressmsg.Name("wildcard_names.names", n)
		if ref != nil {
			return nil, ref
		}
		out = append(out, name)
	}
	return out, nil
}
