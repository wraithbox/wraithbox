// Shared fixtures for the wb-vmd tests: a log that keeps its records, a
// fake VM engine, temporary directories and bundles, and sockets.
import Darwin
import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import GRPCProtobuf
import NIOCore
import Synchronization
import Testing
import WraithBoxVM

@testable import WraithBoxVMD

/// Keeps every log record, parsed.
final class RecordedLog: Sendable {
    private let store = Mutex<[String]>([])

    var log: EventLog {
        EventLog(sink: { line in self.store.withLock { $0.append(line) } }, clock: { "t" })
    }

    var lines: [String] {
        store.withLock { $0 }
    }

    func append(_ line: String) {
        store.withLock { $0.append(line) }
    }

    var records: [[String: Any]] {
        lines.compactMap { line in
            (try? JSONSerialization.jsonObject(with: Data(line.utf8))) as? [String: Any]
        }
    }

    /// The records whose event is `event`.
    func events(_ event: String) -> [[String: Any]] {
        records.filter { $0["event"] as? String == event }
    }

    /// The rules of every refusal record.
    var rules: [String] {
        records.compactMap { $0["rule"] as? String }
    }
}

/// A VM engine that builds fake VMs.
@MainActor
final class FakeEngine: VMEngine {
    var limits = ResourceLimits(cpuCount: 1...8, memoryBytes: (512 << 20)...(16 << 30))
    var configFailure: VMFailure?
    var startFailure: VMFailure?
    /// When set, each start suspends until `releaseStart` is called.
    var holdStart = false
    private(set) var requests: [BootRequest] = []
    private(set) var machines: [FakeVM] = []

    func makeVM(_ request: BootRequest, onState: @escaping VMStateHandler) throws(VMFailure)
        -> any EngineVM
    {
        if let configFailure {
            throw configFailure
        }
        requests.append(request)
        let machine = FakeVM(onState: onState, startFailure: startFailure, hold: holdStart)
        machines.append(machine)
        return machine
    }
}

/// A VM that changes state when asked, and records the stops.
@MainActor
final class FakeVM: EngineVM {
    private let onState: VMStateHandler
    private let startFailure: VMFailure?
    private var hold: Bool
    private var held: CheckedContinuation<Void, Never>?
    private(set) var stops: [String] = []

    init(onState: @escaping VMStateHandler, startFailure: VMFailure?, hold: Bool) {
        self.onState = onState
        self.startFailure = startFailure
        self.hold = hold
    }

    func start() async throws(VMFailure) {
        if let startFailure {
            throw startFailure
        }
        if hold {
            await withCheckedContinuation { held = $0 }
        }
        onState(.running, nil)
    }

    /// Whether a held start is waiting.
    var isHeld: Bool {
        held != nil
    }

    /// Lets a held start finish.
    func releaseStart() {
        hold = false
        held?.resume()
        held = nil
    }

    func requestStop() throws(VMFailure) {
        stops.append("request")
        onState(.stopping, nil)
    }

    func forceStop() async throws(VMFailure) {
        stops.append("force")
        onState(.stopped, nil)
    }

    /// The guest stops on its own.
    func guestStopped() {
        onState(.stopped, nil)
    }

    /// What connect does: open a socketpair, fail, or hold the completion
    /// until `releaseConnects`.
    enum ConnectMode {
        case succeed
        case fail(VMFailure)
        case hold
    }

    var connectMode = ConnectMode.succeed
    /// The ports of every connect, in order.
    private(set) var connects: [UInt32] = []
    /// The connections the fake opened.
    private(set) var connections: [FakeConnection] = []
    private var heldConnects: [(UInt32, GuestConnectHandler)] = []

    var heldCount: Int {
        heldConnects.count
    }

    func connect(toPort port: UInt32, completion: @escaping GuestConnectHandler) {
        connects.append(port)
        switch connectMode {
        case .succeed:
            completion(open(port))
        case .fail(let failure):
            completion(.failure(failure))
        case .hold:
            heldConnects.append((port, completion))
        }
    }

    /// Completes every held connect with a new connection.
    func releaseConnects() {
        let waiting = heldConnects
        heldConnects = []
        for (port, completion) in waiting {
            completion(open(port))
        }
    }

    private func open(_ port: UInt32) -> Result<any GuestConnection, VMFailure> {
        guard let connection = try? FakeConnection(destinationPort: port) else {
            return .failure(VMFailure(description: "socketpair failed"))
        }
        connections.append(connection)
        return .success(connection)
    }
}

/// A guest connection over a stream socketpair: wb-vmd's end, which it
/// passes, and the end that stands in for the guest.
@MainActor
final class FakeConnection: GuestConnection {
    let fileDescriptor: Int32
    let guest: Int32
    let sourcePort: UInt32 = 49152
    let destinationPort: UInt32
    private(set) var closes = 0

    init(destinationPort: UInt32) throws {
        var fds: [Int32] = [0, 0]
        guard socketpair(AF_UNIX, SOCK_STREAM, 0, &fds) == 0 else {
            throw POSIXError(.EMFILE)
        }
        fileDescriptor = fds[0]
        guest = fds[1]
        self.destinationPort = destinationPort
    }

    func close() {
        closes += 1
        Darwin.close(fileDescriptor)
    }

    deinit {
        Darwin.close(guest)
    }
}

/// One record received on the hand-off channel, and its descriptors.
struct ReceivedHandoff: Equatable {
    var record: [UInt8]
    var descriptors: [Int32]
}

/// Receives one record from the test's end of the hand-off channel
/// without waiting, or nil when none is there.
func receiveHandoff(_ fd: Int32) -> ReceivedHandoff? {
    var data = [UInt8](repeating: 0, count: 512)
    var control = [UInt8](repeating: 0, count: 256)
    var controlLength = 0
    let count = data.withUnsafeMutableBytes { dataBytes in
        control.withUnsafeMutableBytes { controlBytes in
            var iov = iovec(iov_base: dataBytes.baseAddress, iov_len: dataBytes.count)
            return withUnsafeMutablePointer(to: &iov) { iovPointer in
                var message = msghdr(
                    msg_name: nil, msg_namelen: 0, msg_iov: iovPointer, msg_iovlen: 1,
                    msg_control: controlBytes.baseAddress,
                    msg_controllen: socklen_t(controlBytes.count), msg_flags: 0)
                let received = recvmsg(fd, &message, MSG_DONTWAIT)
                controlLength = Int(message.msg_controllen)
                return received
            }
        }
    }
    guard count >= 0 else {
        return nil
    }
    return ReceivedHandoff(
        record: Array(data.prefix(count)),
        descriptors: rights(in: Array(control.prefix(controlLength))))
}

/// The descriptors of every SCM_RIGHTS message in control data.
private func rights(in control: [UInt8]) -> [Int32] {
    var descriptors: [Int32] = []
    var offset = 0
    let header = MemoryLayout<cmsghdr>.size
    while offset + header <= control.count {
        let message = control.withUnsafeBytes {
            $0.loadUnaligned(fromByteOffset: offset, as: cmsghdr.self)
        }
        let length = Int(message.cmsg_len)
        guard length >= header, offset + length <= control.count else {
            break
        }
        if message.cmsg_level == SOL_SOCKET, message.cmsg_type == SCM_RIGHTS {
            for at in stride(from: offset + header, to: offset + length, by: 4) {
                descriptors.append(
                    control.withUnsafeBytes { $0.loadUnaligned(fromByteOffset: at, as: Int32.self) }
                )
            }
        }
        offset += (length + 3) & ~3
    }
    return descriptors
}

/// A new temporary directory, removed when the value goes away. Its path
/// is short, for socket paths.
final class TemporaryDirectory: Sendable {
    let path: String

    init() throws {
        var template = Array("/tmp/wb-vmd-XXXXXX".utf8CString)
        guard let made = mkdtemp(&template) else {
            throw POSIXError(.EIO)
        }
        path = realPath(String(cString: made)) ?? String(cString: made)
    }

    deinit {
        try? FileManager.default.removeItem(atPath: path)
    }
}

/// A slot bundle with every bundle file, under `<data>` in a temporary
/// home.
final class BundleFixture {
    let home: TemporaryDirectory
    let path: String

    init() throws {
        home = try TemporaryDirectory()
        path = HostPaths.data(home: home.path) + "/" + HostPaths.bundle(of: .work)
        try FileManager.default.createDirectory(atPath: path, withIntermediateDirectories: true)
        for file in BundleFile.allCases {
            FileManager.default.createFile(
                atPath: path + "/" + file.rawValue, contents: Data("x".utf8))
        }
    }
}

/// A listening AF_UNIX stream socket at `path`.
func listeningSocket(at path: String) throws -> Int32 {
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else {
        throw POSIXError(.EIO)
    }
    var address = sockaddr_un()
    address.sun_family = sa_family_t(AF_UNIX)
    let bytes = Array(path.utf8)
    precondition(bytes.count < MemoryLayout.size(ofValue: address.sun_path))
    withUnsafeMutableBytes(of: &address.sun_path) { buffer in
        buffer.copyBytes(from: bytes)
    }
    let bound = withUnsafePointer(to: &address) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard bound == 0, listen(fd, 8) == 0 else {
        close(fd)
        throw POSIXError(.EADDRINUSE)
    }
    return fd
}

/// A connected AF_UNIX stream socket to `path`, for a raw peer.
func connectedSocket(to path: String) throws -> Int32 {
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    var address = sockaddr_un()
    address.sun_family = sa_family_t(AF_UNIX)
    withUnsafeMutableBytes(of: &address.sun_path) { buffer in
        buffer.copyBytes(from: Array(path.utf8))
    }
    let connected = withUnsafePointer(to: &address) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard connected == 0 else {
        close(fd)
        throw POSIXError(.ECONNREFUSED)
    }
    return fd
}

/// Reads from `fd` until end of file or `timeout` seconds, and returns
/// what arrived.
func readUntilClosed(_ fd: Int32, timeout: Int = 5) -> [UInt8] {
    var timeoutValue = timeval(tv_sec: timeout, tv_usec: 0)
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeoutValue, socklen_t(MemoryLayout<timeval>.size))
    var received: [UInt8] = []
    var buffer = [UInt8](repeating: 0, count: 1024)
    while true {
        let count = buffer.withUnsafeMutableBytes { read(fd, $0.baseAddress, $0.count) }
        guard count > 0 else {
            return received
        }
        received += buffer.prefix(count)
    }
}

/// A gRPC client transport to wb-vmd's socket that runs the version
/// exchange first.
func clientTransport(path: String, version: String, log: EventLog) throws
    -> HTTP2ClientTransport.Posix
{
    var config = HTTP2ClientTransport.Posix.Config.defaults
    config.channelDebuggingCallbacks.onCreateTCPConnection = { channel in
        channel.eventLoop.makeCompletedFuture {
            try channel.pipeline.syncOperations.addHandler(
                HostHandshake.connectingSide(ownVersion: version, log: log), position: .first)
        }
    }
    return try HTTP2ClientTransport.Posix(
        target: .unixDomainSocket(path: path), transportSecurity: .plaintext, config: config)
}

/// The ErrorInfo reason of a refused call.
func refusalReason(_ error: any Error) -> String? {
    guard let rpcError = error as? RPCError, let status = try? rpcError.unpackGoogleRPCStatus()
    else {
        return nil
    }
    return status.details.compactMap { $0.errorInfo?.reason }.first
}
