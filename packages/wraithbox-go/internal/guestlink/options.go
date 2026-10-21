package guestlink

import (
	"context"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// limits are the gRPC limits of every guest connection, on whichever
// side wb-hostd is, client or server. Every field is set, so no limit is
// left at grpc-go's default (S04-08-records-and-bounds, "Host work the
// guest can cause", row "RPCs from `wb-guestd`": values in code).
type limits struct {
	// maxMsgSize caps each message, received and sent. A method that
	// needs less sets a lower cap on its call, as Hello does.
	maxMsgSize int
	// maxHeaderListSize caps the headers and trailers of a call, which
	// hold the peer's status message. grpc-go's default is 16 MiB.
	maxHeaderListSize uint32
	// streamWindow and connWindow are the HTTP/2 flow control windows,
	// static so that grpc-go doesn't grow them: the most a peer can make
	// wb-hostd buffer per stream and per connection. 64 KiB is HTTP/2's
	// floor, which grpc-go doesn't go under.
	streamWindow int32
	connWindow   int32
	// bufferSize is the read and the write buffer of a connection.
	bufferSize int
	// callTimeout is the deadline of a unary call to wb-guestd whose
	// context has none.
	callTimeout time.Duration
	// maxStreams caps the concurrent streams the guest opens on one
	// connection to a wb-hostd server.
	maxStreams uint32
	// keepaliveMinTime is the shortest ping interval a guest client may
	// use. A guest that pings more often has its connection closed.
	keepaliveMinTime time.Duration
	// keepaliveTime and keepaliveTimeout: the server pings a guest
	// client that has been silent this long, and closes the connection
	// when the ping isn't answered in time.
	keepaliveTime    time.Duration
	keepaliveTimeout time.Duration
	// handshakeTimeout bounds the HTTP/2 handshake of a guest client.
	handshakeTimeout time.Duration
}

// guestLimits are the values (S04-08-records-and-bounds, "RPCs from
// `wb-guestd`"). wb-hostd sends nothing large to the guest, and the
// guest's messages are requests and short answers.
var guestLimits = limits{
	maxMsgSize:        64 << 10,
	maxHeaderListSize: 4 << 10,
	streamWindow:      64 << 10,
	connWindow:        64 << 10,
	bufferSize:        16 << 10,
	callTimeout:       10 * time.Second,
	maxStreams:        16,
	keepaliveMinTime:  time.Minute,
	keepaliveTime:     time.Minute,
	keepaliveTimeout:  20 * time.Second,
	handshakeTimeout:  5 * time.Second,
}

// The names wb-hostd gives itself on a guest connection. :authority
// would otherwise come from the dial target, and user-agent names
// grpc-go's version, so the guest learns no host path from either.
// grpc-go appends its own "grpc-go/<version>" to the user agent, which
// no option removes. That version is no secret from the guest: its
// wb-guestd is built from the same module.
const (
	authority = "wb-guestd"
	userAgent = "wb-hostd"
)

// option is one gRPC option and the name of what it sets, so a test can
// check that every limit is set.
type option[T any] struct {
	name string
	opt  T
}

// clientOptions are the options of every gRPC client on a guest
// connection. dial hands out the connection (newClient).
func clientOptions(l limits, dial func(context.Context, string) (net.Conn, error)) []option[grpc.DialOption] {
	return []option[grpc.DialOption]{
		{"dialer", grpc.WithContextDialer(dial)},
		// TLS would add nothing: the host-guest socket is a channel to one
		// VM that only wb-vmd opens, and its peer is guest root either way.
		{"credentials", grpc.WithTransportCredentials(insecure.NewCredentials())},
		{"authority", grpc.WithAuthority(authority)},
		{"user-agent", grpc.WithUserAgent(userAgent)},
		{"retry", grpc.WithDisableRetry()},
		// A Link stays open for later calls. Going idle would close the
		// connection, and the dialer can't open it again.
		{"idle", grpc.WithIdleTimeout(0)},
		{"message size", grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(l.maxMsgSize), grpc.MaxCallSendMsgSize(l.maxMsgSize))},
		{"header list size", grpc.WithMaxHeaderListSize(l.maxHeaderListSize)},
		{"stream window", grpc.WithStaticStreamWindowSize(l.streamWindow)},
		{"connection window", grpc.WithStaticConnWindowSize(l.connWindow)},
		{"read buffer", grpc.WithReadBufferSize(l.bufferSize)},
		{"write buffer", grpc.WithWriteBufferSize(l.bufferSize)},
		{"call deadline", grpc.WithChainUnaryInterceptor(defaultDeadline(l.callTimeout))},
	}
}

// serverOptions are the options of every gRPC server wb-hostd runs on a
// guest connection, with rpc's per-VM bounds as interceptors.
func serverOptions(l limits, rpc *RPCLimiter) []option[grpc.ServerOption] {
	return []option[grpc.ServerOption]{
		{"credentials", grpc.Creds(insecure.NewCredentials())},
		{"receive size", grpc.MaxRecvMsgSize(l.maxMsgSize)},
		{"send size", grpc.MaxSendMsgSize(l.maxMsgSize)},
		{"header list size", grpc.MaxHeaderListSize(l.maxHeaderListSize)},
		{"stream window", grpc.StaticStreamWindowSize(l.streamWindow)},
		{"connection window", grpc.StaticConnWindowSize(l.connWindow)},
		{"read buffer", grpc.ReadBufferSize(l.bufferSize)},
		{"write buffer", grpc.WriteBufferSize(l.bufferSize)},
		{"streams", grpc.MaxConcurrentStreams(l.maxStreams)},
		{"keepalive enforcement", grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: l.keepaliveMinTime, PermitWithoutStream: false,
		})},
		{"keepalive", grpc.KeepaliveParams(keepalive.ServerParameters{
			Time: l.keepaliveTime, Timeout: l.keepaliveTimeout,
		})},
		{"handshake", grpc.ConnectionTimeout(l.handshakeTimeout)},
		{"unary bounds", grpc.ChainUnaryInterceptor(rpc.unary)},
		{"stream bounds", grpc.ChainStreamInterceptor(rpc.stream)},
	}
}

// defaultDeadline gives a unary call without a deadline one of d.
func defaultDeadline(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func dialOptions(opts []option[grpc.DialOption]) []grpc.DialOption {
	out := make([]grpc.DialOption, len(opts))
	for i, o := range opts {
		out[i] = o.opt
	}
	return out
}

// NewServer returns a gRPC server for a guest connection, with the
// limits of every guest connection and rpc's per-VM bounds. rpc is the
// VM's one RPCLimiter, shared by every server on its connections. No
// other code serves on a guest connection.
func NewServer(rpc *RPCLimiter) *grpc.Server {
	opts := serverOptions(guestLimits, rpc)
	out := make([]grpc.ServerOption, len(opts))
	for i, o := range opts {
		out[i] = o.opt
	}
	return grpc.NewServer(out...)
}
