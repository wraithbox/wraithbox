// Package guestsock is wb-guestd's end of the host-guest socket: a
// listener on Port that only the host may reach (S06-12-guest-agent,
// "The port"; S12-01-platform-interfaces, "Host-guest socket on macOS").
//
// The listener keeps serving through everything a guest process can do
// to it (S04-02-vm-provider, "Guest ports below 1024"):
//
//   - A failed accept is logged and skipped, never fatal. A guest process
//     that connects to the guest's own CID makes accept fail with
//     ECONNABORTED (X27-vsock-confinement, condition 3).
//   - A connection from any peer but the host is closed and logged.
//   - Both are logged at most once per window per kind, with the count
//     held back, so a connect flood can't flood the log
//     (SEC13-bounded-resources).
//   - A bind that finds the port in use is retried with a backoff, not
//     fatal, so a restart race doesn't hand the restart to the service
//     manager's throttle. Any other bind error is fatal: it means a broken
//     install, not a race.
//
// The socket itself is per OS (S12-01-platform-interfaces, "Rules"). On
// darwin it is AF_VSOCK through golang.org/x/sys/unix. Linux and Windows
// guests are later, and Listen there returns an error that wraps
// errors.ErrUnsupported.
package guestsock

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"
)

// Port is wb-guestd's vsock port: 1023, the highest port only guest root
// can bind (X27-vsock-confinement, condition 1; SEC08-proj-isolation). A
// project user can't take it while wb-guestd restarts. wb-guestd listens
// on it, and wb-hostd asks wb-vmd to connect to it.
const Port uint32 = 1023

// HostCID is the vsock context ID of the host (VMADDR_CID_HOST). Only a
// connection from it is served.
const HostCID uint32 = 2

// The rules of this package, for the log.
const (
	// RuleAcceptFailed: accept returned an error, such as the
	// ECONNABORTED of a self-CID connect. The listener skips it.
	RuleAcceptFailed = "guestd-accept-failed"
	// RulePeerNotHost: a connection came from a peer other than the
	// host's CID. The listener closes it.
	RulePeerNotHost = "guestd-peer-not-host"
	// RuleBindInUse: the port is in use, as when the old wb-guestd hasn't
	// let it go yet. Listen retries.
	RuleBindInUse = "guestd-bind-in-use"
	// RuleBindFailed: bind failed for any other reason, EACCES included.
	// Listen returns the error, and wb-guestd exits non-zero.
	RuleBindFailed = "guestd-bind-failed"
)

const (
	// logWindow is how often a record of one kind is written at most
	// (#115 item 1).
	logWindow = 10 * time.Second

	// firstBindRetry and maxBindRetry bound the bind backoff, which
	// doubles from the first to the second. launchd throttles a daemon
	// that exits to one start per 10 s, so the backoff stays well below
	// that (S04-02-vm-provider, "Guest ports below 1024").
	firstBindRetry = 100 * time.Millisecond
	maxBindRetry   = 2 * time.Second

	// firstAcceptPause and maxAcceptPause bound the pause after an accept
	// error other than an aborted connection, such as EMFILE, which would
	// otherwise fail again at once.
	firstAcceptPause = 5 * time.Millisecond
	maxAcceptPause   = time.Second
)

// Peer is the address of the other end of an accepted connection.
type Peer struct {
	CID  uint32
	Port uint32
}

// socket is the OS listening socket. accept returns the next connection
// and its peer. close makes a blocked accept return.
type socket interface {
	accept() (net.Conn, Peer, error)
	close() error
	addr() net.Addr
}

// bindFunc binds a listening socket to a port.
type bindFunc func(port uint32) (socket, error)

// Listener is a net.Listener whose Accept returns only connections from
// the host. Accept and Close may run at the same time.
type Listener struct {
	sock socket
	log  *slog.Logger

	acceptErrors logLimiter
	refusedPeers logLimiter

	closeOnce sync.Once
	done      chan struct{}
}

// Listen binds port on every CID and listens. While the port is in use it
// retries, logging each try with RuleBindInUse, until ctx ends. Any other
// bind error is logged with RuleBindFailed and returned.
func Listen(ctx context.Context, log *slog.Logger, port uint32) (*Listener, error) {
	return listen(ctx, log, port, bindPort, sleep)
}

func listen(ctx context.Context, log *slog.Logger, port uint32, bind bindFunc, wait func(context.Context, time.Duration) error) (*Listener, error) {
	delay := firstBindRetry
	for {
		sock, err := bind(port)
		if err == nil {
			log.LogAttrs(ctx, slog.LevelInfo, "listening", slog.Uint64("port", uint64(port)))
			return newListener(sock, log, time.Now), nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			log.LogAttrs(ctx, slog.LevelError, "bind failed", append(errorAttrs(err),
				slog.String("rule", RuleBindFailed), slog.String("decision", "exit"),
				slog.Uint64("port", uint64(port)))...)
			return nil, err
		}
		log.LogAttrs(ctx, slog.LevelWarn, "port in use", append(errorAttrs(err),
			slog.String("rule", RuleBindInUse), slog.String("decision", "retry"),
			slog.Uint64("port", uint64(port)), slog.Duration("retry_in", delay))...)
		if err := wait(ctx, delay); err != nil {
			return nil, err
		}
		delay = min(2*delay, maxBindRetry)
	}
}

func newListener(sock socket, log *slog.Logger, now func() time.Time) *Listener {
	return &Listener{
		sock:         sock,
		log:          log,
		acceptErrors: logLimiter{window: logWindow, now: now},
		refusedPeers: logLimiter{window: logWindow, now: now},
		done:         make(chan struct{}),
	}
}

// Accept returns the next connection from the host. It skips failed
// accepts and closes connections from other peers, logging both, rate
// limited. It returns net.ErrClosed once Close is called.
func (l *Listener) Accept() (net.Conn, error) {
	var pause time.Duration
	for {
		conn, peer, err := l.sock.accept()
		if l.closed() {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, net.ErrClosed
		}
		if err != nil {
			l.logAcceptError(err)
			pause = nextPause(pause, err)
			if !l.sleep(pause) {
				return nil, net.ErrClosed
			}
			continue
		}
		pause = 0
		if peer.CID != HostCID {
			// Only the host may talk to wb-guestd (SEC08-proj-isolation).
			_ = conn.Close()
			l.logRefusedPeer(peer)
			continue
		}
		// Only the host opens these, and it caps its reconnects
		// (S06-11-reconnect-and-recovery, "Reconnect"), so each is logged.
		l.log.LogAttrs(context.Background(), slog.LevelInfo, "connection accepted",
			slog.String("decision", "serve"),
			slog.Uint64("peer_cid", uint64(peer.CID)), slog.Uint64("peer_port", uint64(peer.Port)))
		return conn, nil
	}
}

// Close stops the listener. A blocked Accept returns net.ErrClosed. It
// logs the records the rate limit still holds back.
func (l *Listener) Close() error {
	err := net.ErrClosed
	l.closeOnce.Do(func() {
		close(l.done)
		err = l.sock.close()
		l.logHeld(RuleAcceptFailed, &l.acceptErrors)
		l.logHeld(RulePeerNotHost, &l.refusedPeers)
	})
	return err
}

// Addr is the listening address.
func (l *Listener) Addr() net.Addr { return l.sock.addr() }

func (l *Listener) closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

// sleep waits d, or until Close. It reports false when the listener
// closed.
func (l *Listener) sleep(d time.Duration) bool {
	if d <= 0 {
		return !l.closed()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-l.done:
		return false
	}
}

// nextPause is the pause after an accept error. An aborted or interrupted
// accept used up its connection, so the next accept can go at once. Any
// other error doubles the pause.
func nextPause(prev time.Duration, err error) time.Duration {
	if errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EINTR) {
		return 0
	}
	return min(max(2*prev, firstAcceptPause), maxAcceptPause)
}

func (l *Listener) logAcceptError(err error) {
	ok, held := l.acceptErrors.allow()
	if !ok {
		return
	}
	l.log.LogAttrs(context.Background(), slog.LevelWarn, "accept failed", append(errorAttrs(err),
		slog.String("rule", RuleAcceptFailed), slog.String("decision", "skip"),
		slog.Int("held_back", held))...)
}

func (l *Listener) logRefusedPeer(peer Peer) {
	ok, held := l.refusedPeers.allow()
	if !ok {
		return
	}
	l.log.LogAttrs(context.Background(), slog.LevelWarn, "connection refused",
		slog.String("rule", RulePeerNotHost), slog.String("decision", "close"),
		slog.Uint64("peer_cid", uint64(peer.CID)), slog.Uint64("peer_port", uint64(peer.Port)),
		slog.Int("held_back", held))
}

// logHeld writes one record with the count a limiter still holds back,
// if any.
func (l *Listener) logHeld(rule string, lim *logLimiter) {
	held := lim.flush()
	if held == 0 {
		return
	}
	l.log.LogAttrs(context.Background(), slog.LevelWarn, "records held back at close",
		slog.String("rule", rule), slog.Int("held_back", held))
}

// errorAttrs names an error and, when it carries one, its errno.
func errorAttrs(err error) []slog.Attr {
	attrs := []slog.Attr{slog.String("error", err.Error())}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		attrs = append(attrs, slog.Int("errno", int(errno)))
	}
	return attrs
}

// sleep waits d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
