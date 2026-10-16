// gRPC on the listening socket wb-vmd inherits as fd 3. Its profile
// allows no path outside the bundle, so it can't bind one: the spawner
// binds the socket in `<data>/run/` and passes it (S04-03-self-sandboxing,
// "How `wb-vmd` is started"). grpc-swift's Custom transport takes this
// listener, so each accepted connection passes the host handshake before
// the HTTP/2 pipeline reads from it.
import GRPCNIOTransportHTTP2Posix
import NIOCore
import NIOPosix

/// A listener on an inherited, already listening socket.
struct InheritedListener: HTTP2ServerTransport.ListenerFactory {
    let eventLoopGroup: any EventLoopGroup
    let descriptor: Int32
    let handshake: HostHandshake

    func makeListeningChannel(
        listenerConfigurator: HTTP2ServerTransport.ListenerConfigurator,
        connectionConfigurator: HTTP2ServerTransport.ConnectionConfigurator
    ) async throws -> NIOAsyncChannel<
        HTTP2ServerTransport.ConnectionConfigurator.ConnectionChannel, Never
    > {
        let handshake = handshake
        return try await ServerBootstrap(group: eventLoopGroup)
            .serverChannelInitializer { channel in
                channel.eventLoop.makeCompletedFuture {
                    try channel.pipeline.syncOperations.addHandler(DroppedAcceptFilter())
                }.flatMap {
                    listenerConfigurator.configure(channel: channel)
                }
            }
            .bind(NIOBSDSocket.Handle(descriptor)) { channel in
                handshake.admit(channel).flatMap {
                    connectionConfigurator.configure(channel: channel, tls: .none)
                }
            }
    }
}

/// Drops the error NIO raises when a peer closes a connection before
/// NIO could set it up. NIO has already closed that socket, and the
/// async listener would otherwise end on the error, which ends the
/// server. grpc-swift's own Posix transport does the same.
final class DroppedAcceptFilter: ChannelInboundHandler {
    typealias InboundIn = Any
    typealias InboundOut = Any

    func errorCaught(context: ChannelHandlerContext, error: any Error) {
        if error is NIOFcntlFailedError {
            return
        }
        context.fireErrorCaught(error)
    }
}

/// The gRPC limits of wb-vmd's server, set rather than left at the
/// library defaults (S04-08-records-and-bounds, "Host work the guest can
/// cause"). Every request of VMService is a few bytes.
enum ServerLimits {
    static let maxRequestPayloadSize = 4 * 1024
    static let maxConcurrentStreams = 16

    static var config: HTTP2ServerTransport.Custom<InheritedListener>.Config {
        var config = HTTP2ServerTransport.Custom<InheritedListener>.Config.defaults
        config.rpc.maxRequestPayloadSize = maxRequestPayloadSize
        config.http2.maxConcurrentStreams = maxConcurrentStreams
        return config
    }
}
