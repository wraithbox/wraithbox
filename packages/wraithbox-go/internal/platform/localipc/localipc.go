// Package localipc is the host's local IPC endpoint: a Unix socket in
// <data>/run that only the user can open, where each end checks the
// other's user ID on every connection before anything else
// (S04-architecture, "Inter-process contracts"; SEC12-least-privilege).
//
// The layers, outermost first:
//
//  1. The run directory is mode 0700, owned by the effective user, and
//     not a symbolic link. EnsureRunDir makes it and checks it, and
//     refuses anything else with RuleRunDirUnsafe without changing it.
//  2. The socket file is mode 0600 after the bind.
//  3. The listener reads the peer's credential on every accepted
//     connection, and closes one of another user ID with RulePeerUID
//     before it reads a byte. The dialer checks the listener the same way.
//
// The build version exchange that follows on each connection is in
// internal/hostversion, which has no OS code. It is in internal/platform
// because the endpoint is per OS (S12-01-platform-interfaces, "Local
// IPC"). Linux and Windows return an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package localipc

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
)

// HostdEndpoint is the name of wb-hostd's endpoint,
// <data>/run/wb-hostd.sock, where wb reaches wb-hostd.
const HostdEndpoint = "wb-hostd"

// MaxPathLen is the longest endpoint path: sun_path holds 104 bytes on
// macOS, the last one the terminating NUL.
const MaxPathLen = 103

// Rule names the rule behind a refusal, for the log.
type Rule string

// The rules of this package. Each refusal has its own.
const (
	// RuleRunDirUnsafe: the run directory can't be made, or isn't a
	// directory of the effective user without group or other permission
	// bits, or is a symbolic link (S04-architecture, "Inter-process
	// contracts").
	RuleRunDirUnsafe Rule = "ipc-run-dir-unsafe"
	// RuleEndpointName: an endpoint name doesn't match [a-z][a-z0-9-]*.
	RuleEndpointName Rule = "ipc-endpoint-name"
	// RulePathTooLong: the endpoint path is over MaxPathLen bytes.
	RulePathTooLong Rule = "ipc-path-too-long"
	// RuleEndpointInUse: another listener holds the endpoint's lock, a
	// listener answers at the endpoint path, or the probe of the file
	// there failed other than with a refused connect.
	RuleEndpointInUse Rule = "ipc-endpoint-in-use"
	// RuleEndpointUnsafe: the file at the endpoint path isn't a socket
	// of the effective user, or its lock file isn't a regular file of
	// the effective user. It is left in place.
	RuleEndpointUnsafe Rule = "ipc-endpoint-unsafe"
	// RulePeerUID: the peer's user ID isn't the effective user ID, or
	// its credential can't be read (SEC12-least-privilege).
	RulePeerUID Rule = "ipc-peer-uid"
)

// Refusal is the error of every refusal in this package.
type Refusal struct {
	Rule Rule

	msg string
	err error
}

func (r *Refusal) Error() string {
	s := "localipc: " + string(r.Rule) + ": " + r.msg
	if r.err != nil {
		s += ": " + r.err.Error()
	}
	return s
}

// Unwrap returns the cause, such as the error of a failed lstat.
func (r *Refusal) Unwrap() error { return r.err }

// Peer is the other end of a connection, as the kernel reports it.
type Peer struct {
	UID int
	PID int
}

// Conn is a connection whose peer passed the user ID check.
type Conn struct {
	*net.UnixConn

	peer Peer
}

// Peer returns the checked peer.
func (c *Conn) Peer() Peer { return c.peer }

// Listener accepts connections at one endpoint and hands on only those
// whose peer passed the user ID check. It is a net.Listener, so a gRPC
// server serves it directly. Close removes the socket file.
type Listener struct {
	ln *net.UnixListener
	// lock holds the endpoint's flock for the listener's life.
	lock     *os.File
	log      *slog.Logger
	endpoint string
	// peerOf is peerCredential as it was at Listen, so a test that
	// replaces it doesn't race a listener's goroutine.
	peerOf func(*net.UnixConn) (Peer, error)
}

// Accept returns the next connection whose peer passed the check. It
// closes and logs each other one and goes on, so a refused peer never
// reaches the caller or ends a gRPC Serve.
func (l *Listener) Accept() (net.Conn, error) { return l.AcceptPeer() }

// AcceptPeer is Accept with the concrete type.
func (l *Listener) AcceptPeer() (*Conn, error) {
	for {
		c, err := l.ln.AcceptUnix()
		if err != nil {
			return nil, err
		}
		peer, err := checkPeer(l.log, l.peerOf, c, l.endpoint, "accept")
		if err != nil {
			_ = c.Close()
			continue
		}
		return &Conn{UnixConn: c, peer: peer}, nil
	}
}

// Close stops listening and removes the socket file, then releases the
// endpoint's lock. The socket is gone before another listener can take
// the lock, so Close never removes another listener's socket.
func (l *Listener) Close() error {
	err := l.ln.Close()
	if l.lock != nil {
		if lockErr := l.lock.Close(); err == nil {
			err = lockErr
		}
	}
	return err
}

// Addr returns the endpoint's address.
func (l *Listener) Addr() net.Addr { return l.ln.Addr() }

// ownUID is the user ID every peer must have. The tests replace it.
var ownUID = os.Geteuid

// checkPeer reads the peer's credential with peerOf, and refuses a peer
// of another user ID, or one whose credential can't be read, with
// RulePeerUID (S04-architecture, "Inter-process contracts";
// SEC12-least-privilege). side is "accept" or "dial", for the log.
func checkPeer(log *slog.Logger, peerOf func(*net.UnixConn) (Peer, error), c *net.UnixConn, endpoint, side string) (Peer, error) {
	attrs := []slog.Attr{slog.String("endpoint", endpoint), slog.String("side", side)}
	peer, err := peerOf(c)
	if err != nil {
		return Peer{}, refuse(log, &Refusal{Rule: RulePeerUID, msg: "the peer's credential can't be read", err: err}, attrs...)
	}
	attrs = append(attrs, slog.Int("peer_uid", peer.UID), slog.Int("peer_pid", peer.PID))
	if want := ownUID(); peer.UID != want {
		return Peer{}, refuse(log, &Refusal{Rule: RulePeerUID, msg: fmt.Sprintf("peer user ID %d is not %d", peer.UID, want)}, attrs...)
	}
	logger(log).LogAttrs(context.Background(), slog.LevelDebug, "local IPC peer accepted",
		append(attrs, slog.String("decision", "accept"))...)
	return peer, nil
}

// logger returns log, or slog.Default() for nil.
func logger(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}

// refuse logs r with its rule and returns it.
func refuse(log *slog.Logger, r *Refusal, attrs ...slog.Attr) *Refusal {
	attrs = append([]slog.Attr{
		slog.String("rule", string(r.Rule)),
		slog.String("decision", "refuse"),
		slog.String("reason", r.Error()),
	}, attrs...)
	logger(log).LogAttrs(context.Background(), slog.LevelWarn, "local IPC refused", attrs...)
	return r
}
