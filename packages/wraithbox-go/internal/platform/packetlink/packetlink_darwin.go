package packetlink

import (
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

// peerCheckInterval is how often the transport checks whether the peer
// closed its end. darwin doesn't wake a reader blocked on a datagram
// socket when its peer closes (the measurement of fdchan), so a closed
// peer is seen that much later at most.
const peerCheckInterval = 500 * time.Millisecond

// Transport is the host end of a VM's virtual NIC: an AF_UNIX
// SOCK_DGRAM socket that carries one Ethernet frame per datagram. One
// goroutine may read while another writes.
type Transport struct {
	conn *net.UnixConn
	raw  syscall.RawConn
	eof  atomic.Bool

	stop      chan struct{}
	closeOnce sync.Once
	watcher   sync.WaitGroup
}

// New makes the transport of an inherited end for frames of an MTU. It
// takes f and closes it, also when it fails: the transport holds its own
// close-on-exec copy. It refuses an end that isn't an AF_UNIX SOCK_DGRAM
// socket with RuleSocketType, one without a peer with
// RuleSocketUnconnected, and one whose socket buffers the kernel didn't
// raise far enough with RuleBuffers (S07-02-packet-path, "Packet
// transport" and "Stack").
func New(f *os.File, mtu int) (*Transport, error) {
	if f == nil {
		return nil, errors.New("packetlink: no file")
	}
	defer func() { _ = f.Close() }()
	if err := CheckMTU(mtu); err != nil {
		return nil, err
	}
	if err := checkSocket(f); err != nil {
		return nil, err
	}
	if err := setBuffers(f, mtu); err != nil {
		return nil, err
	}
	conn, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("packetlink: %s: %w", f.Name(), err)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("packetlink: %s: not a Unix socket connection", f.Name())
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		_ = uc.Close()
		return nil, fmt.Errorf("packetlink: %s: %w", f.Name(), err)
	}
	t := &Transport{conn: uc, raw: raw, stop: make(chan struct{})}
	t.watcher.Go(t.watchPeer)
	return t, nil
}

// checkSocket refuses a descriptor that isn't a connected AF_UNIX
// SOCK_DGRAM socket.
func checkSocket(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return fmt.Errorf("packetlink: %s: %w", f.Name(), err)
	}
	var sockType int
	var sa, peer syscall.Sockaddr
	var typeErr, nameErr, peerErr error
	err = rc.Control(func(fd uintptr) {
		sockType, typeErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_TYPE)
		sa, nameErr = syscall.Getsockname(int(fd))
		peer, peerErr = syscall.Getpeername(int(fd))
	})
	if err != nil {
		return fmt.Errorf("packetlink: %s: %w", f.Name(), err)
	}
	_, unix := sa.(*syscall.SockaddrUnix)
	if typeErr != nil || nameErr != nil || !unix || sockType != syscall.SOCK_DGRAM {
		msg := fmt.Sprintf("%s is not an AF_UNIX SOCK_DGRAM socket (type %d)", f.Name(), sockType)
		return refuse(RuleSocketType, msg, errors.Join(typeErr, nameErr), slog.String("file", f.Name()))
	}
	if peerErr != nil || peer == nil {
		return refuse(RuleSocketUnconnected, f.Name()+" has no peer", peerErr, slog.String("file", f.Name()))
	}
	return nil
}

// setBuffers sets SO_SNDBUF and SO_RCVBUF to bufferSizes and checks what
// the kernel reports after.
func setBuffers(f *os.File, mtu int) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return fmt.Errorf("packetlink: %s: %w", f.Name(), err)
	}
	snd, rcv := bufferSizes(mtu)
	var gotSnd, gotRcv int
	var opErr error
	err = rc.Control(func(fd uintptr) {
		opErr = errors.Join(
			syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, snd),
			syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, rcv),
		)
		if opErr != nil {
			return
		}
		gotSnd, opErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF)
		if opErr != nil {
			return
		}
		gotRcv, opErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	})
	if err == nil {
		err = opErr
	}
	if err != nil {
		msg := fmt.Sprintf("socket buffers of %d and %d bytes not set: %v", snd, rcv, err)
		return refuse(RuleBuffers, msg, err, slog.String("file", f.Name()))
	}
	return checkBuffers(gotSnd, gotRcv, mtu)
}

// Buffers reports the socket's SO_SNDBUF and SO_RCVBUF.
func (t *Transport) Buffers() (snd, rcv int, err error) {
	var opErr error
	err = t.raw.Control(func(fd uintptr) {
		snd, opErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF)
		if opErr != nil {
			return
		}
		rcv, opErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	})
	if err == nil {
		err = opErr
	}
	if err != nil {
		return 0, 0, fmt.Errorf("packetlink: socket buffers: %w", err)
	}
	return snd, rcv, nil
}

// ReadFrame reads one datagram into buf and returns its length. A
// datagram longer than buf is cut to len(buf) bytes, so a caller reads
// into a buffer of ReadBufferSize bytes and drops a read that fills it.
// A closed peer reads as io.EOF.
func (t *Transport) ReadFrame(buf []byte) (int, error) {
	var n int
	var readErr error
	err := t.raw.Read(func(fd uintptr) bool {
		for {
			n, readErr = syscall.Read(int(fd), buf)
			if !errors.Is(readErr, syscall.EINTR) {
				break
			}
		}
		return !errors.Is(readErr, syscall.EAGAIN)
	})
	switch {
	case t.eof.Load():
		return 0, io.EOF
	case err != nil:
		return 0, fmt.Errorf("packetlink: read: %w", err)
	case errors.Is(readErr, syscall.ECONNRESET):
		// darwin reports a closed datagram peer once, as ECONNRESET.
		t.eof.Store(true)
		return 0, io.EOF
	case readErr != nil:
		return 0, fmt.Errorf("packetlink: read: %w", readErr)
	}
	return n, nil
}

// WriteFrame sends one frame as one datagram. It makes one attempt and
// never waits: a send the peer's full buffer refused, with ENOBUFS or
// EAGAIN, returns an error that wraps ErrBusy, and the caller decides
// whether to try again.
func (t *Transport) WriteFrame(frame []byte) error {
	var n int
	var writeErr error
	err := t.raw.Write(func(fd uintptr) bool {
		for {
			n, writeErr = syscall.Write(int(fd), frame)
			if !errors.Is(writeErr, syscall.EINTR) {
				break
			}
		}
		// One attempt, also on EAGAIN.
		return true
	})
	switch {
	case err != nil:
		return fmt.Errorf("packetlink: write: %w", err)
	case errors.Is(writeErr, syscall.ENOBUFS) || errors.Is(writeErr, syscall.EAGAIN):
		return fmt.Errorf("%w: %w", ErrBusy, writeErr)
	case writeErr != nil:
		return fmt.Errorf("packetlink: write: %w", writeErr)
	case n != len(frame):
		return fmt.Errorf("packetlink: write: sent %d of %d bytes", n, len(frame))
	}
	return nil
}

// watchPeer closes the socket when the peer has closed its end, so that
// a blocked ReadFrame returns io.EOF. darwin disconnects the survivor of
// a datagram socketpair.
func (t *Transport) watchPeer() {
	tick := time.NewTicker(peerCheckInterval)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
		}
		var peerErr error
		if err := t.raw.Control(func(fd uintptr) { _, peerErr = syscall.Getpeername(int(fd)) }); err != nil {
			return
		}
		if errors.Is(peerErr, syscall.ENOTCONN) {
			t.eof.Store(true)
			_ = t.conn.Close()
			return
		}
	}
}

// Close closes the transport. A ReadFrame blocked on it returns an
// error.
func (t *Transport) Close() error {
	t.closeOnce.Do(func() { close(t.stop) })
	err := t.conn.Close()
	t.watcher.Wait()
	if errors.Is(err, net.ErrClosed) && t.eof.Load() {
		return nil
	}
	return err
}
