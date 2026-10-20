// gRPC on the listening socket wb-vmd inherits as fd 3. Its profile
// allows no path outside the bundle, so it can't bind one: the spawner
// binds the socket in `<data>/run/` and passes it (S04-03-self-sandboxing,
// "How `wb-vmd` is started"). grpc-swift's Custom transport takes this
// listener, so each accepted connection passes the host handshake before
// the HTTP/2 pipeline reads from it.
import Darwin
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
                    try channel.pipeline.syncOperations.addHandler(
                        DroppedAcceptFilter(log: handshake.log))
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

/// Drops the accept errors that NIO keeps listening after: the error NIO
/// raises when a peer closes a connection before NIO could set it up,
/// which grpc-swift's own Posix transport drops too, and the errno values
/// NIO's server channel treats as recoverable, such as EMFILE. The async
/// listener would otherwise end on the error, which ends the server, so
/// a full descriptor table would stop wb-vmd serving for good. Each
/// dropped error is logged, at most once a second with the count held
/// back.
final class DroppedAcceptFilter: ChannelInboundHandler {
    typealias InboundIn = Any
    typealias InboundOut = Any

    /// NIO's list of the accept errors its server channel recovers from.
    static let recoverable: Set<Int32> = [ECONNABORTED, EMFILE, ENFILE, ENOBUFS, ENOMEM]

    /// The least time between two log records of dropped errors.
    static let logInterval = TimeAmount.seconds(1)

    private let log: EventLog
    private var lastLog: NIODeadline?
    private var heldBack = 0

    init(log: EventLog) {
        self.log = log
    }

    func errorCaught(context: ChannelHandlerContext, error: any Error) {
        guard let kind = Self.droppedKind(error) else {
            context.fireErrorCaught(error)
            return
        }
        let now = NIODeadline.now()
        if let lastLog, now - lastLog < Self.logInterval {
            heldBack += 1
            return
        }
        log.info(
            "accept-error-dropped", ["error": .string(kind), "held_back": .int(Int64(heldBack))])
        lastLog = now
        heldBack = 0
    }

    /// The name of an error to drop, or nil to pass it on.
    static func droppedKind(_ error: any Error) -> String? {
        if error is NIOFcntlFailedError {
            return "fcntl"
        }
        if let io = error as? IOError, recoverable.contains(io.errnoCode) {
            return String(cString: strerror(io.errnoCode))
        }
        return nil
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
