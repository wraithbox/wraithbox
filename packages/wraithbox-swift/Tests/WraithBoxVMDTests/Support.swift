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
    private(set) var requests: [BootRequest] = []
    private(set) var machines: [FakeVM] = []

    func makeVM(_ request: BootRequest, onState: @escaping VMStateHandler) throws(VMFailure)
        -> any EngineVM
    {
        if let configFailure {
            throw configFailure
        }
        requests.append(request)
        let machine = FakeVM(onState: onState, startFailure: startFailure)
        machines.append(machine)
        return machine
    }
}

/// A VM that changes state when asked, and records the stops.
@MainActor
final class FakeVM: EngineVM {
    private let onState: VMStateHandler
    private let startFailure: VMFailure?
    private(set) var stops: [String] = []

    init(onState: @escaping VMStateHandler, startFailure: VMFailure?) {
        self.onState = onState
        self.startFailure = startFailure
    }

    func start() async throws(VMFailure) {
        if let startFailure {
            throw startFailure
        }
        onState(.running, nil)
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
