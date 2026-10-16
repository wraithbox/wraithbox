// What happens on each connection to wb-vmd's gRPC socket before gRPC
// sees it: the peer check, then the version exchange
// (S04-architecture, "Inter-process contracts" and "The version
// record"). A refused connection is closed and logged
// with its rule, and gRPC never reads a byte of it.
import NIOCore
import NIOPosix

/// Reads the credential of a connection's peer. Tests replace it.
public typealias PeerLookup = @Sendable (any Channel) -> EventLoopFuture<PeerCredential?>

/// The checks every connection to wb-vmd passes.
public struct HostHandshake: Sendable {
    /// How long a peer has to send its version record. A value in code
    /// (S04-architecture, "The version record").
    public static let defaultTimeout = TimeAmount.seconds(5)

    public let ownVersion: String
    public let peerCheck: PeerCheck
    public let lookup: PeerLookup
    public let timeout: TimeAmount
    public let log: EventLog

    public init(
        ownVersion: String, peerCheck: PeerCheck = PeerCheck(),
        lookup: @escaping PeerLookup = HostHandshake.socketOptionLookup,
        timeout: TimeAmount = HostHandshake.defaultTimeout, log: EventLog
    ) {
        self.ownVersion = ownVersion
        self.peerCheck = peerCheck
        self.lookup = lookup
        self.timeout = timeout
        self.log = log
    }

    /// Checks the peer of an accepted connection, and on success puts the
    /// version exchange in front of its pipeline. A refused connection is
    /// closed here, and the future fails.
    public func admit(_ channel: any Channel) -> EventLoopFuture<Void> {
        let check = peerCheck
        let log = log
        let ownVersion = ownVersion
        let timeout = timeout
        return lookup(channel).flatMapThrowing { credential in
            let peer: PeerCredential
            do throws(ConnectionRefusal) {
                peer = try check.check(credential)
            } catch {
                log.refusal(
                    "connection-refused", rule: error.rule.rawValue,
                    peerFields(credential).merging(["detail": .string(error.detail)]) { $1 })
                channel.close(promise: nil)
                throw error
            }
            let handler = VersionExchangeHandler(
                ownVersion: ownVersion, timeout: timeout, log: log, peer: peer)
            try channel.pipeline.syncOperations.addHandler(handler, position: .first)
        }
    }

    /// The version exchange for the side that connects: tests, and the
    /// harness of the needs-vm test. Add it first in the pipeline, before
    /// the connection is active.
    public static func connectingSide(
        ownVersion: String, timeout: TimeAmount = defaultTimeout, log: EventLog
    )
        -> any ChannelHandler
    {
        VersionExchangeHandler(ownVersion: ownVersion, timeout: timeout, log: log, peer: nil)
    }

    /// The default lookup: LOCAL_PEERCRED and LOCAL_PEERPID on the
    /// connection's socket.
    public static let socketOptionLookup: PeerLookup = { channel in
        guard let provider = channel as? any SocketOptionProvider else {
            return channel.eventLoop.makeSucceededFuture(nil)
        }
        let credential: EventLoopFuture<xucred> = provider.unsafeGetSocketOption(
            level: SocketOptionLevel(SOL_LOCAL), name: SocketOptionName(LOCAL_PEERCRED))
        let pid: EventLoopFuture<Int32> = provider.unsafeGetSocketOption(
            level: SocketOptionLevel(SOL_LOCAL), name: SocketOptionName(LOCAL_PEERPID))
        return credential.and(pid)
            .map { PeerCredential.from($0, pid: $1) }
            .recover { _ in nil }
    }
}

/// The peer's user ID and process ID as log fields.
func peerFields(_ peer: PeerCredential?) -> [String: LogValue] {
    guard let peer else {
        return [:]
    }
    return ["peer_uid": .uint(peer.uid), "peer_pid": .int(Int64(peer.pid))]
}

/// Sends the version record, then holds every byte the peer sends, and
/// every write of the handlers behind it, until the peer's record is read
/// and checked. So nothing but the record reaches a peer before it is
/// accepted. On success it releases the writes, passes on what followed
/// the record, and leaves the pipeline. On a refusal it closes the
/// connection.
final class VersionExchangeHandler: ChannelDuplexHandler, RemovableChannelHandler {
    typealias InboundIn = ByteBuffer
    typealias InboundOut = ByteBuffer
    typealias OutboundIn = Any
    typealias OutboundOut = Any

    private enum State {
        case idle
        case waiting
        case accepted
        case refused
    }

    private let ownVersion: String
    private let timeout: TimeAmount
    private let log: EventLog
    private let peer: PeerCredential?
    private var state = State.idle
    private var received: [UInt8] = []
    private var heldWrites: [(NIOAny, EventLoopPromise<Void>?)] = []
    private var timer: Scheduled<Void>?

    init(ownVersion: String, timeout: TimeAmount, log: EventLog, peer: PeerCredential?) {
        self.ownVersion = ownVersion
        self.timeout = timeout
        self.log = log
        self.peer = peer
    }

    func handlerAdded(context: ChannelHandlerContext) {
        if context.channel.isActive {
            start(context: context)
        }
    }

    func handlerRemoved(context: ChannelHandlerContext) {
        timer?.cancel()
    }

    func channelActive(context: ChannelHandlerContext) {
        start(context: context)
        context.fireChannelActive()
    }

    func channelInactive(context: ChannelHandlerContext) {
        timer?.cancel()
        context.fireChannelInactive()
    }

    func write(context: ChannelHandlerContext, data: NIOAny, promise: EventLoopPromise<Void>?) {
        switch state {
        case .accepted:
            context.write(data, promise: promise)
        case .idle, .waiting:
            heldWrites.append((data, promise))
        case .refused:
            promise?.fail(ChannelError.ioOnClosedChannel)
        }
    }

    func flush(context: ChannelHandlerContext) {
        if state == .accepted {
            context.flush()
        }
    }

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        guard state == .waiting else {
            return
        }
        var buffer = Self.unwrapInboundIn(data)
        received += buffer.readBytes(length: buffer.readableBytes) ?? []
        switch VersionRecord.parse(received) {
        case .needMore:
            return
        case .malformed(let why):
            refuse(context: context, ConnectionRefusal(.versionMalformed, why))
        case .unstamped(let length):
            decide(context: context, peerVersion: nil, consumed: length)
        case .version(let version, let length):
            decide(context: context, peerVersion: version, consumed: length)
        }
    }

    private func start(context: ChannelHandlerContext) {
        guard state == .idle else {
            return
        }
        state = .waiting
        let record = context.channel.allocator.buffer(bytes: VersionRecord.encode(ownVersion))
        context.writeAndFlush(NIOAny(record), promise: nil)
        timer = context.eventLoop.assumeIsolated().scheduleTask(in: timeout) {
            self.refuse(
                context: context,
                ConnectionRefusal(.versionTimeout, "no version record within \(self.timeout)"))
        }
    }

    private func decide(context: ChannelHandlerContext, peerVersion: String?, consumed: Int) {
        do {
            try VersionRecord.check(peer: peerVersion, own: ownVersion)
        } catch {
            refuse(context: context, error)
            return
        }
        state = .accepted
        timer?.cancel()
        log.info(
            "connection-accepted",
            peerFields(peer).merging(["peer_version": .string(peerVersion ?? "")]) { $1 })
        let rest = Array(received[consumed...])
        received = []
        let writes = heldWrites
        heldWrites = []
        for (data, promise) in writes {
            context.write(data, promise: promise)
        }
        context.flush()
        if !rest.isEmpty {
            context.fireChannelRead(
                Self.wrapInboundOut(context.channel.allocator.buffer(bytes: rest)))
        }
        context.pipeline.syncOperations.removeHandler(context: context, promise: nil)
    }

    private func refuse(context: ChannelHandlerContext, _ refusal: ConnectionRefusal) {
        guard state == .waiting else {
            return
        }
        state = .refused
        timer?.cancel()
        received = []
        let writes = heldWrites
        heldWrites = []
        for (_, promise) in writes {
            promise?.fail(ChannelError.ioOnClosedChannel)
        }
        log.refusal(
            "connection-refused", rule: refusal.rule.rawValue,
            peerFields(peer).merging([
                "detail": .string(refusal.detail), "own_version": .string(ownVersion),
            ]) { $1 })
        context.close(promise: nil)
    }
}
