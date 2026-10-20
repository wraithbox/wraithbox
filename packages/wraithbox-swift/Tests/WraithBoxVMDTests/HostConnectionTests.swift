// C11-host-daemons, "Peer check and version record": the version
// record, the peer user ID check on a socketpair, and both on wb-vmd's
// gRPC socket, where a refused peer never reaches a handler and the next
// connection is still served.
import Darwin
import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import NIOCore
import Synchronization
import Testing
import WraithBoxRPC
import WraithBoxVM

@testable import WraithBoxVMD

@Suite("Version record")
struct VersionRecordTests {
    @Test("a record round-trips")
    func roundTrip() {
        let record = VersionRecord.encode("1.2.3+abc")
        #expect(record == Array("WBV1".utf8) + [9] + Array("1.2.3+abc".utf8))
        #expect(VersionRecord.parse(record) == .version("1.2.3+abc", length: record.count))
        #expect(
            VersionRecord.parse(record + [0x50, 0x52])
                == .version("1.2.3+abc", length: record.count))
    }

    @Test("an empty version is sent as an unstamped record")
    func unstamped() {
        #expect(VersionRecord.encode("") == Array("WBV1".utf8) + [0])
        #expect(VersionRecord.parse(Array("WBV1".utf8) + [0]) == .unstamped(length: 5))
        #expect(VersionRecord.encode(String(repeating: "a", count: 65)) == Array("WBV1".utf8) + [0])
    }

    @Test("a short prefix needs more bytes", arguments: [0, 1, 4, 5, 9])
    func needMore(count: Int) {
        let record = VersionRecord.encode("1.2.3+abc")
        #expect(VersionRecord.parse(Array(record.prefix(count))) == .needMore)
    }

    static let magic: [UInt8] = Array("WBV1".utf8)
    static let malformedRecords: [[UInt8]] = [
        Array("WBV2".utf8) + [1, 0x41],
        Array("XB".utf8),
        magic + [65] + [UInt8](repeating: 0x41, count: 65),
        magic + [255],
        magic + [3, 0x41, 0x0A, 0x41],
        magic + [2, 0xC3, 0xA9],
        magic + [1, 0x7F],
    ]

    @Test("a malformed record is refused", arguments: malformedRecords)
    func malformed(bytes: [UInt8]) {
        guard case .malformed = VersionRecord.parse(bytes) else {
            Issue.record("\(bytes) was not refused")
            return
        }
    }

    @Test("the versions compare as exact bytes, and an unstamped side is refused")
    func check() throws {
        try VersionRecord.check(peer: "1.0.0", own: "1.0.0")
        #expect(throws: ConnectionRefusal(.versionMismatch, "peer 1.0.0+x, own 1.0.0")) {
            try VersionRecord.check(peer: "1.0.0+x", own: "1.0.0")
        }
        #expect(throws: ConnectionRefusal(.versionUnstamped, "peer unstamped, own unstamped")) {
            try VersionRecord.check(peer: nil, own: "")
        }
        #expect(throws: ConnectionRefusal(.versionUnstamped, "peer 1.0.0, own unstamped")) {
            try VersionRecord.check(peer: "1.0.0", own: "")
        }
    }

    @Test("a peer version outside the form is refused with guest-version-form, as in Go")
    func checkForm() {
        for peer in ["dev", "1.0", "v1.0.0", "1.0.0+", "1.0.0 x"] {
            #expect(throws: ConnectionRefusal(.versionForm, "peer \(peer), own 1.0.0")) {
                try VersionRecord.check(peer: peer, own: "1.0.0")
            }
        }
        #expect(throws: ConnectionRefusal(.versionUnstamped, "peer dev, own dev")) {
            try VersionRecord.check(peer: "dev", own: "dev")
        }
    }

    @Test(
        "arbitrary bytes never yield a version that isn't 1 to 64 printable ASCII bytes, and the parser reads no more than the bound"
    )
    func arbitraryBytes() {
        var generator = SystemRandomNumberGenerator()
        for _ in 0..<20_000 {
            let count = Int.random(in: 0...80, using: &generator)
            var bytes = (0..<count).map { _ in UInt8.random(in: 0...255, using: &generator) }
            if Bool.random(using: &generator) {
                bytes = Array("WBV1".utf8) + bytes
            }
            switch VersionRecord.parse(bytes) {
            case .version(let version, let length):
                #expect((1...64).contains(version.utf8.count))
                #expect(version.utf8.allSatisfy { (0x20...0x7E).contains($0) })
                #expect(length <= VersionRecord.maxLength)
                #expect(
                    VersionRecord.parse(Array(bytes.prefix(length)))
                        == .version(version, length: length))
                // Accepted only when equal to the own version and in the form.
                let inForm = WraithBoxVersion.isVersionForm(Array(version.utf8))
                for own in ["1.0.0", version] {
                    let accepted = (try? VersionRecord.check(peer: version, own: own)) != nil
                    #expect(accepted == (version == own && inForm))
                }
            case .unstamped(let length):
                #expect(length == 5)
            case .needMore:
                #expect(bytes.count < VersionRecord.maxLength)
            case .malformed:
                break
            }
        }
    }
}

@Suite("Peer check")
struct PeerCheckTests {
    @Test("a socketpair's peer is this process, and is accepted")
    func ownPeer() throws {
        let pair = try makeSocketPair(SOCK_STREAM)
        defer { closeAll([pair.0, pair.1]) }
        let peer = try #require(PeerCredential.read(fd: pair.0))
        #expect(peer == PeerCredential(uid: getuid(), pid: getpid()))
        #expect(try PeerCheck().check(peer) == peer)
    }

    @Test("a peer with another user ID, or none, is refused with ipc-peer-uid")
    func otherPeer() throws {
        let pair = try makeSocketPair(SOCK_STREAM)
        defer { closeAll([pair.0, pair.1]) }
        let real = try #require(PeerCredential.read(fd: pair.0))
        // The stubbed lookup: the same socket, another user.
        let stubbed = PeerCredential(uid: real.uid + 1, pid: real.pid)
        #expect(
            throws: ConnectionRefusal(.peerUID, "peer user ID \(stubbed.uid) is not \(getuid())")
        ) {
            try PeerCheck().check(stubbed)
        }
        #expect(throws: ConnectionRefusal(.peerUID, "the peer's credential can't be read")) {
            try PeerCheck().check(nil)
        }
    }

    @Test("a credential of another version is not read")
    func credentialVersion() {
        var credential = xucred()
        credential.cr_version = u_int(XUCRED_VERSION) + 1
        #expect(PeerCredential.from(credential, pid: 1) == nil)
    }
}

/// A wb-vmd gRPC server on a socket in a temporary directory, with a
/// fake engine and a counting peer lookup.
final class ServerFixture: Sendable {
    let directory: String
    let socketPath: String
    let recorded = RecordedLog()
    let lookups = Counter()
    private let task: Task<Void, any Error>
    private let keep: TemporaryDirectory

    /// `refuseLookups` makes the first that many peer lookups return
    /// another user ID.
    @MainActor
    init(version: String = "1.0.0", refuseLookups: Int = 0, timeout: TimeAmount = .seconds(5))
        throws
    {
        keep = try TemporaryDirectory()
        directory = keep.path
        socketPath = directory + "/vmd.sock"
        let fd = try listeningSocket(at: socketPath)
        let lookups = lookups
        let log = recorded.log
        let lookup: PeerLookup = { channel in
            let count = lookups.next()
            return HostHandshake.socketOptionLookup(channel).map { credential in
                guard count > refuseLookups, let credential else {
                    return credential.map { PeerCredential(uid: $0.uid + 1, pid: $0.pid) }
                }
                return credential
            }
        }
        let handshake = HostHandshake(
            ownVersion: version, lookup: lookup, timeout: timeout, log: log)
        let supervisor = VMSupervisor(
            engine: FakeEngine(), slot: .work, bundlePath: "/nonexistent", log: log)
        // No test here calls ConnectGuest, so the hand-off channel is none.
        let connector = GuestConnector(
            supervisor: supervisor, handoff: HandoffChannel(fd: -1), log: log)
        let service = VMServiceHandler(supervisor: supervisor, connector: connector, log: log)
        task = Task {
            try await VMDaemon.serveGRPC(
                descriptor: fd, version: version, service: service, log: log,
                handshake: handshake)
        }
    }

    deinit {
        task.cancel()
    }

    /// Calls GetState as a client of `version`.
    func getState(version: String) async throws -> Wraithbox_Vmd_V1_GetStateResponse {
        let transport = try clientTransport(
            path: socketPath, version: version, log: RecordedLog().log)
        return try await withGRPCClient(transport: transport) { grpc in
            try await Wraithbox_Vmd_V1_VMService.Client(wrapping: grpc).getState(
                Wraithbox_Vmd_V1_GetStateRequest(), options: .init(timeout: .seconds(5)))
        }
    }

    /// Opens WatchState `count` times, one after the other on one
    /// connection, each read for its first state and then cancelled.
    /// Returns how many were served. The server learns of a cancel a moment
    /// after the client moves on, so a refused watch is tried again for up
    /// to half a second.
    func watchOneAfterAnother(_ count: Int, version: String) async throws -> Int {
        let transport = try clientTransport(
            path: socketPath, version: version, log: RecordedLog().log)
        return try await withGRPCClient(transport: transport) { grpc in
            let client = Wraithbox_Vmd_V1_VMService.Client(wrapping: grpc)
            var served = 0
            for _ in 0..<count {
                for _ in 0..<50 {
                    let first = try? await client.watchState(Wraithbox_Vmd_V1_WatchStateRequest()) {
                        response in
                        for try await message in response.messages {
                            return message.state.state
                        }
                        return Wraithbox_Vmd_V1_State.unspecified
                    }
                    if first == .stopped {
                        served += 1
                        break
                    }
                    try await Task.sleep(for: .milliseconds(10))
                }
            }
            return served
        }
    }

    func waitForEvent(_ event: String) async throws {
        for _ in 0..<200 where recorded.events(event).isEmpty {
            try await Task.sleep(for: .milliseconds(25))
        }
    }
}

/// A thread-safe counter.
final class Counter: Sendable {
    private let value = Mutex(0)

    func next() -> Int {
        value.withLock { count in
            count += 1
            return count
        }
    }
}

extension CallOptions {
    init(timeout: Duration) {
        self = .defaults
        self.timeout = timeout
    }
}

@Suite("Host connections", .serialized)
struct HostConnectionTests {
    @Test("a peer of the same version gets an answer, and is logged with its version")
    func sameVersion() async throws {
        let server = try await ServerFixture()
        let response = try await server.getState(version: "1.0.0")
        #expect(response.state.state == .stopped)
        let accepted = try #require(server.recorded.events("connection-accepted").first)
        #expect(accepted["peer_version"] as? String == "1.0.0")
        #expect(accepted["peer_uid"] as? Int == Int(getuid()))
    }

    @Test("a cancelled WatchState frees its slot, so watches never run out")
    func watchCancelled() async throws {
        let server = try await ServerFixture()
        let count = VMSupervisor.maxWatchers * 2
        #expect(try await server.watchOneAfterAnother(count, version: "1.0.0") == count)
    }

    @Test("a peer of another version is refused on both sides, before any handler runs")
    func otherVersion() async throws {
        let server = try await ServerFixture()
        await #expect(throws: RPCError.self) {
            _ = try await server.getState(version: "1.0.1")
        }
        try await server.waitForEvent("connection-refused")
        #expect(server.recorded.rules == ["peer-version-mismatch"])
        #expect(server.recorded.events("connection-accepted").isEmpty)
    }

    @Test("an unstamped peer is refused with peer-version-unstamped")
    func unstampedPeer() async throws {
        let server = try await ServerFixture()
        await #expect(throws: RPCError.self) {
            _ = try await server.getState(version: "")
        }
        try await server.waitForEvent("connection-refused")
        #expect(server.recorded.rules == ["peer-version-unstamped"])
    }

    @Test("a peer whose version is outside the form is refused with guest-version-form")
    func peerOutsideForm() async throws {
        let server = try await ServerFixture()
        await #expect(throws: RPCError.self) {
            _ = try await server.getState(version: "dev")
        }
        try await server.waitForEvent("connection-refused")
        #expect(server.recorded.rules == ["guest-version-form"])
        #expect(server.recorded.events("connection-accepted").isEmpty)
    }

    @Test(
        "a peer with another user ID is closed with ipc-peer-uid, and the next connection is served"
    )
    func otherUser() async throws {
        let server = try await ServerFixture(refuseLookups: 1)
        await #expect(throws: RPCError.self) {
            _ = try await server.getState(version: "1.0.0")
        }
        #expect(server.recorded.rules == ["ipc-peer-uid"])
        let response = try await server.getState(version: "1.0.0")
        #expect(response.state.state == .stopped)
    }

    @Test("a refused peer gets nothing: not even the version record")
    func refusedPeerGetsNothing() async throws {
        let server = try await ServerFixture(refuseLookups: 1)
        let fd = try connectedSocket(to: server.socketPath)
        defer { close(fd) }
        #expect(readUntilClosed(fd).isEmpty)
    }

    @Test("a peer that never sends its record is refused with peer-version-timeout")
    func silentPeer() async throws {
        let server = try await ServerFixture(timeout: .milliseconds(200))
        let fd = try connectedSocket(to: server.socketPath)
        defer { close(fd) }
        let received = readUntilClosed(fd)
        #expect(received == VersionRecord.encode("1.0.0"))
        try await server.waitForEvent("connection-refused")
        #expect(server.recorded.rules == ["peer-version-timeout"])
    }

    @Test("a peer that keeps sending after a bad magic is refused with peer-version-malformed")
    func garbagePeer() async throws {
        let server = try await ServerFixture()
        let fd = try connectedSocket(to: server.socketPath)
        defer { close(fd) }
        let garbage = [UInt8](repeating: 0x41, count: 4096)
        _ = garbage.withUnsafeBytes { write(fd, $0.baseAddress, $0.count) }
        _ = readUntilClosed(fd)
        try await server.waitForEvent("connection-refused")
        #expect(server.recorded.rules == ["peer-version-malformed"])
    }
}
