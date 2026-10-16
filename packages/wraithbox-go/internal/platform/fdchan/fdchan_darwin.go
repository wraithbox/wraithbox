package fdchan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// kernelMaxFDs is the most descriptors darwin lets one message carry:
	// one SCM_RIGHTS control message of up to 254 descriptors (measured
	// on macOS 27; 255 fails at the sender with EINVAL, and so does a
	// second control message). darwin installs every descriptor of a
	// message in the receiver even when the control buffer cuts them off,
	// and the ones cut off can't be closed. So the control buffer has
	// room for all of them, more than the two that S07-05-stream-hand-off
	// ("Message form") asks for, and Recv closes each one of a refused
	// message. A test fails when the limit changes.
	kernelMaxFDs = 254

	// controlRoomFDs is how many descriptors the control buffer holds:
	// four times kernelMaxFDs, so a later macOS that raises the limit
	// still fits, or at least ends the channel instead of leaking quietly.
	controlRoomFDs = 4 * kernelMaxFDs

	// bufferSlack is what a message needs in the socket buffers beside
	// its record: the control data of one descriptor and the sender's
	// address. darwin refuses a datagram over SO_SNDBUF with EMSGSIZE,
	// and its default is 2048 bytes (net.local.dgram.maxdgram).
	bufferSlack = 1024

	// queuedRecords is how many records of the maximum size the receive
	// buffer holds before a send fails with ENOBUFS.
	queuedRecords = 16

	// peerCheckInterval is how often a waiting Recv checks whether the
	// peer closed, so it returns io.EOF that much later at most.
	peerCheckInterval = 500 * time.Millisecond
)

// controlSpace is CMSG_SPACE for controlRoomFDs descriptors.
var controlSpace = syscall.CmsgSpace(controlRoomFDs * fdSize)

// Channel is one end of a descriptor channel. Send and Recv may run at
// the same time from different goroutines.
type Channel struct {
	conn      *net.UnixConn
	raw       syscall.RawConn
	maxRecord int
	eof       atomic.Bool

	mu           sync.Mutex // guards readDeadline, the socket's read deadline, and failed
	readDeadline time.Time
	failed       error // the refusal that ended the channel
}

// Socketpair makes an AF_UNIX SOCK_DGRAM socketpair for records of up to
// maxRecord bytes, with close-on-exec set on both ends. It raises
// SO_SNDBUF and SO_RCVBUF on both ends so that a record of maxRecord
// bytes with one descriptor fits. The caller wraps an end it keeps with
// New, and passes the other to a child, which wraps it with New.
func Socketpair(maxRecord int) (a, b *os.File, err error) {
	if err := checkMax(maxRecord); err != nil {
		return nil, nil, err
	}
	// darwin has no SOCK_CLOEXEC. Holding ForkLock until both ends are
	// close-on-exec keeps a fork in another goroutine from inheriting one.
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, fmt.Errorf("fdchan: socketpair: %w", err)
	}
	for _, fd := range fds {
		if err := setBuffers(fd, maxRecord); err != nil {
			_ = syscall.Close(fds[0])
			_ = syscall.Close(fds[1])
			return nil, nil, err
		}
	}
	return os.NewFile(uintptr(fds[0]), "fdchan"), os.NewFile(uintptr(fds[1]), "fdchan"), nil
}

func setBuffers(fd, maxRecord int) error {
	snd := maxRecord + bufferSlack
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, snd); err != nil {
		return fmt.Errorf("fdchan: SO_SNDBUF %d: %w", snd, err)
	}
	rcv := queuedRecords * snd
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, rcv); err != nil {
		return fmt.Errorf("fdchan: SO_RCVBUF %d: %w", rcv, err)
	}
	return nil
}

// New makes a channel of one end, an end of Socketpair or one inherited
// from the parent, for records of up to maxRecord bytes. It takes f and
// closes it, also when it fails: the channel holds its own close-on-exec
// copy, in non-blocking mode so that deadlines work. An end that isn't
// an AF_UNIX SOCK_DGRAM socket is refused with RuleSocketType, and one
// without a peer with RuleSocketUnconnected.
func New(f *os.File, maxRecord int) (*Channel, error) {
	if f == nil {
		return nil, errors.New("fdchan: no file")
	}
	defer func() { _ = f.Close() }()
	if err := checkMax(maxRecord); err != nil {
		return nil, err
	}
	family, sockType, err := SocketType(f)
	if err != nil || family != syscall.AF_UNIX || sockType != syscall.SOCK_DGRAM {
		r := &Refusal{
			Rule: RuleSocketType,
			msg:  fmt.Sprintf("%s is not an AF_UNIX SOCK_DGRAM socket (family %d, type %d)", f.Name(), family, sockType),
			err:  err,
		}
		logRefusal(r, slog.String("file", f.Name()))
		return nil, r
	}
	if err := connected(f); err != nil {
		r := &Refusal{Rule: RuleSocketUnconnected, msg: f.Name() + " has no peer", err: err}
		logRefusal(r, slog.String("file", f.Name()))
		return nil, r
	}
	conn, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("fdchan: %s: %w", f.Name(), err)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("fdchan: %s: not a Unix socket connection", f.Name())
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		_ = uc.Close()
		return nil, fmt.Errorf("fdchan: %s: %w", f.Name(), err)
	}
	return &Channel{conn: uc, raw: raw, maxRecord: maxRecord}, nil
}

// SocketType reports the socket family and type of a descriptor, such as
// syscall.AF_UNIX and syscall.SOCK_STREAM, for a caller that checks a
// received descriptor (S04-02-vm-provider, "Hand-off on macOS": "the
// socket type is wrong"). A descriptor that isn't a socket, or whose
// family the syscall package doesn't know, is an error.
func SocketType(c syscall.Conn) (family, sockType int, err error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, 0, fmt.Errorf("fdchan: %w", err)
	}
	var opErr error
	err = rc.Control(func(fd uintptr) {
		sockType, opErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_TYPE)
		if opErr != nil {
			opErr = fmt.Errorf("getsockopt SO_TYPE: %w", opErr)
			return
		}
		var sa syscall.Sockaddr
		sa, opErr = syscall.Getsockname(int(fd))
		if opErr != nil {
			opErr = fmt.Errorf("getsockname: %w", opErr)
			return
		}
		family = sockaddrFamily(sa)
	})
	if err == nil {
		err = opErr
	}
	if err != nil {
		return 0, 0, fmt.Errorf("fdchan: socket type: %w", err)
	}
	return family, sockType, nil
}

// connected checks that the socket has a peer: a socketpair end does,
// until the peer closes.
func connected(c syscall.Conn) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var peerErr error
	if err := rc.Control(func(fd uintptr) { _, peerErr = syscall.Getpeername(int(fd)) }); err != nil {
		return err
	}
	if peerErr != nil {
		return fmt.Errorf("getpeername: %w", peerErr)
	}
	return nil
}

func sockaddrFamily(sa syscall.Sockaddr) int {
	switch sa.(type) {
	case *syscall.SockaddrUnix:
		return syscall.AF_UNIX
	case *syscall.SockaddrInet4:
		return syscall.AF_INET
	case *syscall.SockaddrInet6:
		return syscall.AF_INET6
	default:
		return syscall.AF_UNSPEC
	}
}

// Send sends one record, with the descriptor of fd when fd isn't nil. An
// empty record, or one over the maximum, is refused with RuleRecordSize
// before the send. It makes one attempt: a send that fails, with ENOBUFS
// or EAGAIN when the peer's buffer is full, or after the write deadline,
// returns the error and is never retried, so records never go out of
// order (S07-05-stream-hand-off, "Transport"). The caller logs a failed
// send with its own rule.
func (c *Channel) Send(record []byte, fd syscall.Conn) error {
	if n := len(record); n < 1 || n > c.maxRecord {
		r := &Refusal{
			Rule: RuleRecordSize,
			msg:  fmt.Sprintf("record of %d bytes not sent, the channel takes 1 to %d", n, c.maxRecord),
		}
		logRefusal(r, slog.String("direction", "send"), slog.Int("bytes", n))
		return r
	}
	if fd == nil {
		return c.sendmsg(record, nil)
	}
	rc, err := fd.SyscallConn()
	if err != nil {
		return fmt.Errorf("fdchan: send: %w", err)
	}
	var sendErr error
	// Control keeps fd open until the send returns.
	err = rc.Control(func(sfd uintptr) {
		sendErr = c.sendmsg(record, syscall.UnixRights(int(sfd)))
	})
	if err != nil {
		return fmt.Errorf("fdchan: send: %w", err)
	}
	return sendErr
}

func (c *Channel) sendmsg(record, oob []byte) error {
	var n int
	var sendErr error
	err := c.raw.Write(func(fd uintptr) bool {
		n, sendErr = syscall.SendmsgN(int(fd), record, oob, nil, 0)
		// One attempt, also on EAGAIN.
		return true
	})
	switch {
	case err != nil:
		return fmt.Errorf("fdchan: send: %w", err)
	case sendErr != nil:
		return fmt.Errorf("fdchan: send: %w", sendErr)
	case n != len(record):
		return fmt.Errorf("fdchan: send: sent %d of %d bytes", n, len(record))
	}
	return nil
}

// Recv receives one message. It reads into a buffer one byte larger
// than the maximum, with a control buffer that holds every descriptor
// one message can carry, and refuses a message as decide says, after
// closing every descriptor in it (S07-05-stream-hand-off, "Message
// form"). Each refusal is logged with its rule and returned as a
// *Refusal. A closed peer reads as io.EOF once the records it sent are
// read, and so does every Recv after it.
//
// The channel stays usable after a refusal, except after one with
// RuleControlTruncated: the kernel may then have put descriptors in this
// process that no one can name, so Recv closes the channel, and every
// later Recv returns that refusal again.
//
// darwin doesn't wake a waiting reader when a datagram peer closes. So
// while it waits, Recv checks every peerCheckInterval whether the peer
// is still connected.
func (c *Channel) Recv() (*Message, error) {
	buf := make([]byte, c.maxRecord+1)
	oob := make([]byte, controlSpace)
	peerClosed := false
	for {
		if err := c.ended(); err != nil {
			return nil, err
		}
		user := c.armReadDeadline(peerClosed)
		m, err := c.recvOnce(buf, oob, peerClosed)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			return m, err
		}
		if !user.IsZero() && !time.Now().Before(user) {
			return nil, err
		}
		peerClosed = c.peerClosed()
	}
}

// ended returns io.EOF after the peer closed, the refusal that ended
// the channel after one did, and nil otherwise.
func (c *Channel) ended() error {
	if c.eof.Load() {
		return io.EOF
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed
}

// armReadDeadline sets the socket's read deadline for one wait of Recv:
// the caller's deadline or the next peer check, whichever is first, and
// none for the last read after the peer closed. It returns the caller's
// deadline.
func (c *Channel) armReadDeadline(peerClosed bool) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	wait := time.Time{}
	if !peerClosed {
		wait = time.Now().Add(peerCheckInterval)
		if !c.readDeadline.IsZero() && c.readDeadline.Before(wait) {
			wait = c.readDeadline
		}
	}
	// An error here is a closed channel, which the read reports.
	_ = c.conn.SetReadDeadline(wait)
	return c.readDeadline
}

// peerClosed reports whether the peer has closed its end: darwin
// disconnects the survivor of a datagram socketpair.
func (c *Channel) peerClosed() bool {
	var err error
	if ctlErr := c.raw.Control(func(fd uintptr) { _, err = syscall.Getpeername(int(fd)) }); ctlErr != nil {
		return false
	}
	return errors.Is(err, syscall.ENOTCONN)
}

// recvOnce reads one message, waiting until the deadline armReadDeadline
// set. After the peer closed, it doesn't wait: no message means io.EOF.
//
// darwin has no MSG_CMSG_CLOEXEC, so it holds ForkLock across recvmsg
// and setting close-on-exec on each descriptor that arrived: a fork in
// another goroutine never inherits one.
func (c *Channel) recvOnce(buf, oob []byte, peerClosed bool) (*Message, error) {
	var r received
	var d decision
	var recvErr, dropErr error
	err := c.raw.Read(func(fd uintptr) bool {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		r, recvErr = recvmsg(int(fd), buf, oob)
		switch {
		case errors.Is(recvErr, syscall.EAGAIN) && peerClosed:
			recvErr = syscall.ECONNRESET
			return true
		case errors.Is(recvErr, syscall.EAGAIN):
			return false
		case fdTableFull(recvErr):
			dropErr = dropRedelivered(int(fd), buf, oob, c.maxRecord)
			return true
		case recvErr != nil:
			return true
		}
		d = decide(darwinControl, r, c.maxRecord)
		for _, rfd := range d.closeFDs {
			syscall.CloseOnExec(rfd)
		}
		if d.keep >= 0 {
			syscall.CloseOnExec(d.keep)
		}
		return true
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("fdchan: recv: %w", err)
	case errors.Is(recvErr, syscall.ECONNRESET):
		// darwin reports a closed datagram peer once, as ECONNRESET.
		c.eof.Store(true)
		return nil, io.EOF
	case fdTableFull(recvErr):
		return nil, c.refuseFDTableFull(recvErr, dropErr)
	case recvErr != nil:
		return nil, fmt.Errorf("fdchan: recv: %w", recvErr)
	case d.rule != "":
		return nil, c.refuse(r, d)
	}
	m := &Message{Record: buf[:r.n:r.n]}
	if d.keep >= 0 {
		m.File = os.NewFile(uintptr(d.keep), "fdchan-received")
	}
	return m, nil
}

// recvmsg reads one datagram, again when a signal interrupts it: the
// netpoller's kqueue is edge-triggered and doesn't report a datagram
// that was already queued again.
func recvmsg(fd int, buf, oob []byte) (received, error) {
	for {
		n, controlLen, flags, _, err := syscall.Recvmsg(fd, buf, oob, 0)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return received{}, err
		}
		return received{n: n, control: oob[:controlLen], flags: flags}, nil
	}
}

// fdTableFull reports a recvmsg that failed because the descriptors of
// the message don't fit in the descriptor table. darwin then returns
// EMSGSIZE (measured on macOS 27) and EMFILE elsewhere.
func fdTableFull(err error) bool {
	return errors.Is(err, syscall.EMSGSIZE) || errors.Is(err, syscall.EMFILE)
}

// dropRedelivered reads and drops the message whose descriptors didn't
// fit. darwin discards the descriptors and leaves the datagram at the
// head of the queue, where the next recvmsg would return it without
// them. Any descriptor it does carry is closed.
func dropRedelivered(fd int, buf, oob []byte, maxRecord int) error {
	r, err := recvmsg(fd, buf, oob)
	if err != nil {
		return err
	}
	d := decide(darwinControl, r, maxRecord)
	for _, rfd := range d.closeFDs {
		_ = syscall.Close(rfd)
	}
	if d.keep >= 0 {
		_ = syscall.Close(d.keep)
	}
	return nil
}

// refuseFDTableFull logs and returns the refusal of a message whose
// descriptors didn't fit in the descriptor table. When the message
// couldn't be dropped, the channel ends, so that it is never delivered
// without its descriptor.
func (c *Channel) refuseFDTableFull(recvErr, dropErr error) error {
	ref := &Refusal{
		Rule: RuleFDTableFull,
		msg:  "the descriptors of a message don't fit in the descriptor table, so it is dropped",
		err:  recvErr,
	}
	attrs := []slog.Attr{slog.String("direction", "recv"), slog.String("error", recvErr.Error())}
	if dropErr != nil {
		ref.msg = "the descriptors of a message don't fit in the descriptor table, and it can't be dropped: " + dropErr.Error()
		attrs = append(attrs, slog.Bool("channel_closed", true))
		logRefusal(ref, attrs...)
		return c.fail(ref)
	}
	logRefusal(ref, attrs...)
	return ref
}

// fail ends the channel with err: it closes the socket, and every later
// Recv returns err.
func (c *Channel) fail(err error) error {
	c.mu.Lock()
	if c.failed == nil {
		c.failed = err
	}
	c.mu.Unlock()
	_ = c.conn.Close()
	return err
}

// refuse closes every descriptor of a refused message, and logs and
// returns the refusal.
func (c *Channel) refuse(r received, d decision) error {
	for _, fd := range d.closeFDs {
		_ = syscall.Close(fd)
	}
	ref := &Refusal{
		Rule: d.rule,
		msg: fmt.Sprintf("message of %d bytes (maximum %d), %d control bytes, flags %#x refused, %d descriptors closed",
			r.n, c.maxRecord, len(r.control), r.flags, len(d.closeFDs)),
	}
	attrs := []slog.Attr{
		slog.String("direction", "recv"),
		slog.Int("bytes", r.n),
		slog.Int("max", c.maxRecord),
		slog.Int("control_bytes", len(r.control)),
		slog.Int("flags", r.flags),
		slog.Int("closed", len(d.closeFDs)),
	}
	if d.rule != RuleControlTruncated {
		logRefusal(ref, attrs...)
		return ref
	}
	// Never expected: the control buffer holds every descriptor the
	// kernel lets one message carry. The kernel may have put descriptors
	// in this process that the cut control data doesn't name, so the
	// channel ends instead of going on with them.
	attrs = append(attrs, slog.Int("lost", d.lost), slog.Bool("channel_closed", true))
	logRefusal(ref, attrs...)
	return c.fail(ref)
}

// SetReadDeadline sets the deadline for Recv, as net.Conn does. A Recv
// that waits picks the new deadline up at once.
func (c *Channel) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	// Wake a waiting Recv, which arms the new deadline.
	return c.conn.SetReadDeadline(time.Now())
}

// SetWriteDeadline sets the deadline for Send. A send never blocks, so
// the deadline only matters once it has passed.
func (c *Channel) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// Close closes the channel. A Recv blocked on it returns an error.
func (c *Channel) Close() error {
	return c.conn.Close()
}

// checkMax checks a channel's maximum record size.
func checkMax(maxRecord int) error {
	if maxRecord < 1 || maxRecord > MaxRecordLimit {
		return fmt.Errorf("fdchan: maximum record size %d is outside 1 to %d", maxRecord, MaxRecordLimit)
	}
	return nil
}

// logRefusal logs a refusal with its rule. The caller turns it into an
// audit record where its spec asks for one.
func logRefusal(r *Refusal, attrs ...slog.Attr) {
	attrs = append([]slog.Attr{
		slog.String("rule", string(r.Rule)),
		slog.String("decision", "refuse"),
	}, attrs...)
	slog.Default().LogAttrs(context.Background(), slog.LevelWarn, "fdchan message refused", attrs...)
}
