// Package grpccreds runs the build version exchange as the handshake of
// a gRPC connection over a local IPC endpoint (S04-architecture, "The
// version record"; S04-08-records-and-bounds, "Between host processes").
//
// gRPC runs each server handshake off its accept loop, so a peer that
// never sends doesn't hold up others, and a failed handshake closes the
// connection before any RPC handler runs. The client dials through
// localipc (ClientOptions), so the run directory and peer checks apply
// on both ends.
package grpccreds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/localipc"
)

// protocol names the handshake in AuthInfo and ProtocolInfo.
const protocol = "wraithbox-host"

// RulePeerUnchecked: the connection didn't come through localipc, so no
// peer check ran on it. The handshake refuses it rather than serve a
// peer nobody checked (SEC12-least-privilege).
const RulePeerUnchecked = "ipc-peer-unchecked"

// AuthInfo is what the handshake learned about the peer, for logs
// through peer.FromContext.
type AuthInfo struct {
	credentials.CommonAuthInfo

	// Peer is the peer's user ID and process ID, as localipc checked them.
	Peer localipc.Peer
	// Version is the peer's build version, equal to this side's own.
	Version string
}

// AuthType names the handshake.
func (AuthInfo) AuthType() string { return protocol }

// Credentials is a gRPC TransportCredentials whose handshake is the
// version exchange.
type Credentials struct {
	log *slog.Logger
	own string

	mu   sync.Mutex
	last *hostversion.Refusal
}

// New returns the credentials for a server or a client with the build
// version own, normally version.Build().
func New(log *slog.Logger, own string) *Credentials {
	return &Credentials{log: log, own: own}
}

// LastRefusal returns the version refusal of the latest handshake, or
// nil when the latest handshake passed or none has run. gRPC turns a
// handshake error into a status message, so a client reads the refusal
// here to name both versions (S04-08-records-and-bounds, "Between host
// processes"). Each client gets its own *Credentials from New, so the
// refusal is that client's.
func (c *Credentials) LastRefusal() *hostversion.Refusal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// ServerHandshake runs the version exchange on a connection the server
// accepted. Afterwards it gives gRPC Timeout again to read the HTTP/2
// preface: gRPC clears the deadline once the preface is in.
func (c *Credentials) ServerHandshake(raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	info, err := c.handshake(context.Background(), raw)
	if err != nil {
		return nil, nil, err
	}
	if err := raw.SetDeadline(time.Now().Add(hostversion.Timeout)); err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("grpccreds: set the preface deadline: %w", err)
	}
	return raw, info, nil
}

// ClientHandshake runs the version exchange on a connection the client
// dialed, within ctx.
func (c *Credentials) ClientHandshake(ctx context.Context, _ string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	info, err := c.handshake(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, info, nil
}

func (c *Credentials) handshake(ctx context.Context, raw net.Conn) (AuthInfo, error) {
	pc, ok := raw.(*localipc.Conn)
	if !ok {
		_ = raw.Close()
		err := fmt.Errorf("grpccreds: %s: a %T didn't come through localipc", RulePeerUnchecked, raw)
		logger(c.log).LogAttrs(ctx, slog.LevelWarn, "host connection refused",
			slog.String("rule", RulePeerUnchecked), slog.String("decision", "refuse"), slog.String("reason", err.Error()))
		return AuthInfo{}, err
	}
	v, err := hostversion.Exchange(ctx, c.log, raw, c.own)
	r, _ := errors.AsType[*hostversion.Refusal](err)
	c.mu.Lock()
	c.last = r
	c.mu.Unlock()
	if err != nil {
		return AuthInfo{}, err
	}
	return AuthInfo{
		// The connection is a Unix socket in a user-only directory, with
		// the peer's user ID checked on both ends, as gRPC's own local
		// credentials rate a Unix socket.
		SecurityLevel: credentials.PrivacyAndIntegrity,
		Peer:          pc.Peer(),
		Version:       v,
	}, nil
}

// Info names the protocol.
func (c *Credentials) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: protocol}
}

// Clone returns credentials with the same logger and version.
func (c *Credentials) Clone() credentials.TransportCredentials { return New(c.log, c.own) }

// OverrideServerName does nothing: a local endpoint has no server name.
func (*Credentials) OverrideServerName(string) error { return nil }

// ClientOptions returns the dial options of a client of the endpoint
// <dir>/<name>.sock: the credentials, and a dialer through localipc.Dial,
// which checks the run directory and the listener's user ID. The target
// passed to grpc.NewClient is "passthrough:///<name>".
func ClientOptions(log *slog.Logger, creds *Credentials, dir, name string) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return localipc.Dial(ctx, log, dir, name)
		}),
	}
}

func logger(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}
