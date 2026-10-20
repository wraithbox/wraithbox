// ConnectGuest (S04-02-vm-provider, "The VM provider passes descriptors,
// not bytes", "Hand-off on macOS", "Caps and closing"). wb-vmd opens a
// host-guest socket connection to a guest port below 1024, sends its
// descriptor to wb-hostd on the hand-off channel in one HandoffMessage
// record, closes its own copy at once (X18-vsock-handoff, condition 4),
// and only then answers. It never reads or writes a byte of the
// connection, so guest bytes stay out of its code (SEC12-least-privilege).
// The host opens every connection: wb-vmd registers no host listener
// (S04-02-vm-provider, "The host opens every connection"), and the
// SwiftLint rule vmd_refused_device_class keeps the listener class out of
// the sources.
import Darwin
import WraithBoxRPC

/// A connection the framework opened to a guest port: a Unix stream
/// socket to Virtualization's service process, and the vsock ports.
@MainActor
public protocol GuestConnection: AnyObject, Sendable {
    var fileDescriptor: Int32 { get }
    var sourcePort: UInt32 { get }
    var destinationPort: UInt32 { get }
    /// Closes wb-vmd's copy of the descriptor.
    func close()
}

/// Called once with the connection the framework opened, or the error.
public typealias GuestConnectHandler = @MainActor (Result<any GuestConnection, VMFailure>) -> Void

/// The hand-off channel: an AF_UNIX SOCK_DGRAM socket that wb-hostd
/// passed as fd 4 (S04-03-self-sandboxing, "How `wb-vmd` is started").
public struct HandoffChannel: Sendable {
    public let fd: Int32

    public init(fd: Int32) {
        self.fd = fd
    }

    /// Sends `record` as one datagram with `descriptor` attached by
    /// SCM_RIGHTS. It makes one attempt and doesn't wait: a peer whose
    /// buffer is full fails the send with ENOBUFS or EAGAIN instead of
    /// blocking wb-vmd. Returns the errno of a failed send, or nil.
    public func send(_ record: [UInt8], descriptor: Int32) -> Int32? {
        // CMSG_LEN(sizeof(int)) and CMSG_SPACE(sizeof(int)): the header,
        // aligned to 4 bytes on Darwin, then one descriptor.
        let dataOffset = Self.align(MemoryLayout<cmsghdr>.size)
        var control = [UInt8](
            repeating: 0, count: dataOffset + Self.align(MemoryLayout<Int32>.size))
        control.withUnsafeMutableBytes { raw in
            let header = cmsghdr(
                cmsg_len: socklen_t(dataOffset + MemoryLayout<Int32>.size), cmsg_level: SOL_SOCKET,
                cmsg_type: SCM_RIGHTS)
            raw.storeBytes(of: header, toByteOffset: 0, as: cmsghdr.self)
            raw.storeBytes(of: descriptor, toByteOffset: dataOffset, as: Int32.self)
        }
        var bytes = record
        while true {
            let sent = bytes.withUnsafeMutableBytes { data in
                control.withUnsafeMutableBytes { ctl in
                    var iov = iovec(iov_base: data.baseAddress, iov_len: data.count)
                    return withUnsafeMutablePointer(to: &iov) { iovPointer in
                        var message = msghdr(
                            msg_name: nil, msg_namelen: 0, msg_iov: iovPointer, msg_iovlen: 1,
                            msg_control: ctl.baseAddress, msg_controllen: socklen_t(ctl.count),
                            msg_flags: 0)
                        return sendmsg(fd, &message, MSG_DONTWAIT)
                    }
                }
            }
            if sent == record.count {
                return nil
            }
            if sent < 0 && errno == EINTR {
                continue
            }
            return sent < 0 ? errno : EMSGSIZE
        }
    }

    private static func align(_ length: Int) -> Int {
        (length + 3) & ~3
    }
}

/// Opens guest connections for ConnectGuest and passes them on.
@MainActor
public final class GuestConnector {
    /// The most connects in flight: started, and not yet passed or
    /// failed. A value in code (S04-02-vm-provider, "Caps and closing";
    /// SEC13-bounded-resources). The caps per VM and per project are
    /// wb-hostd's.
    nonisolated static let maxInFlight = 8

    /// How long a connect may take before ConnectGuest is refused. A value
    /// in code.
    public nonisolated static let defaultTimeout = Duration.seconds(5)

    /// Every guest port the host connects to is below this one
    /// (S04-02-vm-provider, "Guest ports below 1024").
    nonisolated static let firstUnreservedPort: UInt32 = 1024

    private let supervisor: VMSupervisor
    private let handoff: HandoffChannel
    private let log: EventLog
    private let timeout: Duration

    /// Connects started and not yet passed or failed. A connect that timed
    /// out keeps its slot until the framework completes it, so a framework
    /// that never answers can't make wb-vmd start more than the cap.
    public private(set) var inFlight = 0

    public init(
        supervisor: VMSupervisor, handoff: HandoffChannel, log: EventLog,
        timeout: Duration = GuestConnector.defaultTimeout
    ) {
        self.supervisor = supervisor
        self.handoff = handoff
        self.log = log
        self.timeout = timeout
    }

    /// Opens a connection to `port` in the guest of `generation` and
    /// sends it to wb-hostd as request `requestID`. Returns once the
    /// record is sent and wb-vmd's copy is closed. Nothing is sent when
    /// it throws.
    public func connect(generation: UInt64, requestID: UInt64, port: UInt32)
        async throws(VMRefusal)
    {
        guard port < Self.firstUnreservedPort else {
            throw VMRefusal(
                .guestPortNotReserved, .invalidArgument,
                "guest port \(port) is not below \(Self.firstUnreservedPort)")
        }
        guard requestID != 0 else {
            throw VMRefusal(.requestID, .invalidArgument, "request_id 0")
        }
        let vm = try supervisor.runningVM(generation: generation)
        guard inFlight < Self.maxInFlight else {
            throw VMRefusal(
                .connectCap, .resourceExhausted, "\(Self.maxInFlight) connects are in flight")
        }
        inFlight += 1
        let connection = try await open(vm, port: port)
        defer {
            // X18-vsock-handoff, condition 4: the passed descriptor keeps
            // working without wb-vmd's copy.
            connection.close()
            inFlight -= 1
        }
        try pass(connection, generation: generation, requestID: requestID)
    }

    /// Opens the connection, within the timeout. The slot of a connect
    /// that failed is free when this throws, and the slot of one that
    /// timed out once the framework completes it.
    private func open(_ vm: any EngineVM, port: UInt32) async throws(VMRefusal)
        -> any GuestConnection
    {
        let pending = PendingConnect()
        let outcome = await withCheckedContinuation { continuation in
            pending.start(continuation)
            // Set before the connect, which may complete at once.
            pending.timer = Task { [timeout] in
                try? await Task.sleep(for: timeout)
                _ = pending.finish(.timedOut)
            }
            vm.connect(toPort: port) { [weak self] result in
                guard !pending.finish(.completed(result)) else {
                    return
                }
                // The connect timed out before. A late connection is closed
                // unused, and its slot freed now.
                if case .success(let late) = result {
                    late.close()
                }
                self?.inFlight -= 1
                self?.log.info("guest-connect-late", ["guest_port": .uint(port)])
            }
        }
        switch outcome {
        case .completed(.success(let connection)):
            return connection
        case .completed(.failure(let failure)):
            inFlight -= 1
            throw VMRefusal(.connectFailed, .failedPrecondition, failure.description)
        case .timedOut:
            throw VMRefusal(
                .connectTimeout, .failedPrecondition,
                "no connection to guest port \(port) within \(timeout)")
        }
    }

    /// Sends the record with the connection's descriptor.
    private func pass(_ connection: any GuestConnection, generation: UInt64, requestID: UInt64)
        throws(VMRefusal)
    {
        // A stop or a new start while the connect ran: the record would
        // name a generation the receiver no longer waits for.
        guard supervisor.status.generation == generation, supervisor.status.state == .running
        else {
            throw VMRefusal(
                .notRunning, .failedPrecondition,
                "generation \(generation) stopped while the connect ran")
        }
        var message = Wraithbox_Vmd_V1_HandoffMessage()
        message.requestID = requestID
        message.generation = generation
        message.direction = .hostToGuest
        message.sourcePort = connection.sourcePort
        message.destinationPort = connection.destinationPort
        let record: [UInt8]
        do {
            record = try message.serializedBytes()
        } catch {
            throw VMRefusal(.handoffSendFailed, .failedPrecondition, "encode: \(error)")
        }
        if let failed = handoff.send(record, descriptor: connection.fileDescriptor) {
            throw VMRefusal(
                .handoffSendFailed, .failedPrecondition,
                "sendmsg: \(String(cString: strerror(failed)))")
        }
        log.info(
            "guest-connection-passed",
            [
                "request_id": .uint(requestID), "generation": .uint(generation),
                "source_port": .uint(connection.sourcePort),
                "destination_port": .uint(connection.destinationPort),
            ])
    }
}

/// How a connect ended for ConnectGuest.
private enum ConnectOutcome: Sendable {
    case completed(Result<any GuestConnection, VMFailure>)
    case timedOut
}

/// One connect: whichever of the framework's completion and the timer
/// comes first resumes the waiting call.
@MainActor
private final class PendingConnect {
    private var continuation: CheckedContinuation<ConnectOutcome, Never>?
    private var finished = false
    var timer: Task<Void, Never>?

    func start(_ continuation: CheckedContinuation<ConnectOutcome, Never>) {
        self.continuation = continuation
    }

    /// Resumes the call with `outcome` and returns true, or returns false
    /// when it was resumed already.
    func finish(_ outcome: ConnectOutcome) -> Bool {
        guard !finished else {
            return false
        }
        finished = true
        timer?.cancel()
        continuation?.resume(returning: outcome)
        continuation = nil
        return true
    }
}
