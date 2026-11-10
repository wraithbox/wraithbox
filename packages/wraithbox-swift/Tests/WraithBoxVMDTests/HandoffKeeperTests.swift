// C11-host-daemons, "Hand-off acknowledgment": with a real hand-off
// socketpair, wb-vmd keeps its copy of each descriptor it sends until
// wb-hostd's HandoffAck for the record arrives, closes it at the deadline
// without one, and refuses and logs an acknowledgment of the wrong form
// or for a descriptor it doesn't keep (S07-05-stream-hand-off,
// "Transport").
import Darwin
import Foundation
import SwiftProtobuf
import Testing
import WraithBoxRPC

@testable import WraithBoxVMD

/// Sends `record` as one datagram from the test's end of the hand-off
/// channel, as wb-hostd's receiver does.
func sendRecord(_ fd: Int32, _ record: [UInt8]) {
    let sent = record.withUnsafeBytes { send(fd, $0.baseAddress, $0.count, 0) }
    #expect(sent == record.count, "send: \(String(cString: strerror(errno)))")
}

/// The HandoffAck wb-hostd's receiver sends for `received`.
func ack(for received: ReceivedHandoff, accepted: Bool = true) throws
    -> Wraithbox_Vmd_V1_HandoffAck
{
    let message = try Wraithbox_Vmd_V1_HandoffMessage(serializedBytes: received.record)
    var ack = Wraithbox_Vmd_V1_HandoffAck()
    ack.kind = message.kind
    ack.requestID = message.requestID
    ack.generation = message.generation
    ack.accepted = accepted
    return ack
}

/// Sends the HandoffAck for `received` from the test's end.
func sendAck(_ fd: Int32, for received: ReceivedHandoff, accepted: Bool = true) throws {
    try sendRecord(fd, ack(for: received, accepted: accepted).serializedBytes())
}

/// A HandoffAck for request 7 of generation 1, changed by `change`.
func ackRecord(_ change: (inout Wraithbox_Vmd_V1_HandoffAck) -> Void) throws -> [UInt8] {
    var ack = Wraithbox_Vmd_V1_HandoffAck()
    ack.kind = .connection
    ack.requestID = 7
    ack.generation = 1
    ack.accepted = true
    change(&ack)
    return try ack.serializedBytes()
}

@Suite("Hand-off acknowledgment")
@MainActor
struct HandoffKeeperTests {
    @Test("a connection is kept until its acknowledgment, accepted or refused, and then closed")
    func keptUntilAck() async throws {
        for accepted in [true, false] {
            let (fixture, machine) = try await runningFixture()
            try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
            let connection = try #require(machine.connections.first)
            #expect(connection.closes == 0)
            #expect(fixture.connector.inFlight == 1)
            #expect(fixture.keeper.keptCount == 1)

            let received = try #require(receiveHandoff(fixture.handoff.host))
            defer { closeAll(received.descriptors) }
            try sendAck(fixture.handoff.host, for: received, accepted: accepted)
            #expect(
                try await closeShows { connection.closes == 1 },
                "the acknowledgment did not close wb-vmd's copy within 2 s")
            #expect(fixture.connector.inFlight == 0)
            #expect(fixture.keeper.keptCount == 0)
            let logged = try #require(fixture.recorded.events("handoff-acknowledged").last)
            #expect(logged["request_id"] as? Int == 7)
            #expect(logged["host_decision"] as? String == (accepted ? "accept" : "refuse"))
        }
    }

    @Test("without an acknowledgment the copy is closed at the deadline, and a late one is refused")
    func deadline() async throws {
        let fixture = try ServiceFixture(handoffDeadline: .milliseconds(100))
        try await fixture.supervisor.start(
            generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC, provisioning: false)
        let network = try #require(receiveHandoff(fixture.handoff.host))
        closeAll(network.descriptors)
        let machine = try #require(fixture.engine.machines.first)
        try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        let connection = try #require(machine.connections.first)
        #expect(connection.closes == 0)
        #expect(
            try await closeShows { connection.closes == 1 && fixture.keeper.keptCount == 0 },
            "the deadline did not close wb-vmd's copies within 2 s")
        #expect(fixture.connector.inFlight == 0)
        let timeouts = fixture.recorded.events("handoff-ack-timeout")
        #expect(timeouts.count == 2)
        #expect(timeouts.allSatisfy { $0["rule"] as? String == "handoff-ack-timeout" })
        #expect(timeouts.contains { $0["kind"] as? String == "network" })
        #expect(timeouts.contains { $0["request_id"] as? Int == 7 })

        let received = try #require(receiveHandoff(fixture.handoff.host))
        closeAll(received.descriptors)
        try sendAck(fixture.handoff.host, for: received)
        #expect(
            try await closeShows {
                fixture.recorded.rules.contains(HandoffAckRule.unexpected.rawValue)
            }, "the late acknowledgment was not refused within 2 s")
        #expect(connection.closes == 1)
    }

    @Test("the packet transport's host end is kept until its acknowledgment")
    func networkKept() async throws {
        let fixture = try ServiceFixture()
        try await fixture.supervisor.start(
            generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC, provisioning: false)
        #expect(fixture.keeper.keptCount == 1)
        let received = try #require(receiveHandoff(fixture.handoff.host))
        defer { closeAll(received.descriptors) }
        #expect(try ack(for: received).kind == .network)
        try sendAck(fixture.handoff.host, for: received)
        #expect(
            try await closeShows { fixture.keeper.keptCount == 0 },
            "the acknowledgment did not close the host end within 2 s")
        let logged = try #require(fixture.recorded.events("handoff-acknowledged").first)
        #expect(logged["kind"] as? String == "network")
        #expect(logged["generation"] as? Int == 1)
    }

    @Test(
        "an acknowledgment of the wrong form, or for nothing kept, is refused with its rule",
        arguments: [
            ("generation 0", HandoffAckRule.malformed),
            ("request 0", .malformed),
            ("network with a request", .malformed),
            ("no kind", .malformed),
            ("unknown kind", .malformed),
            ("unknown field", .malformed),
            ("not protobuf", .malformed),
            ("empty", .malformed),
            ("over 256 bytes", .malformed),
            ("with a descriptor", .malformed),
            ("other request", .unexpected),
            ("other generation", .unexpected),
            ("network not kept", .unexpected),
        ])
    func refused(name: String, rule: HandoffAckRule) async throws {
        let (fixture, machine) = try await runningFixture()
        try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        let connection = try #require(machine.connections.first)
        let received = try #require(receiveHandoff(fixture.handoff.host))
        defer { closeAll(received.descriptors) }
        let passedPeer = try sendBad(name, to: fixture.handoff.host)
        #expect(
            try await closeShows { fixture.recorded.rules.contains(rule.rawValue) },
            "no \(rule.rawValue) within 2 s")
        if let passedPeer {
            // The test's own copy is closed only now, so the socket's peer
            // shows the close once wb-vmd has closed the copy it received.
            close(passedPeer.sent)
            #expect(
                try await closeShows { streamPeerClosed(passedPeer.peer) },
                "the passed socket is still open in wb-vmd")
            close(passedPeer.peer)
        }
        #expect(connection.closes == 0)
        #expect(fixture.keeper.keptCount == 1)
        // The wait goes on: the right acknowledgment still closes the copy.
        try sendAck(fixture.handoff.host, for: received)
        #expect(
            try await closeShows { connection.closes == 1 },
            "the acknowledgment after the refusal did not close the copy within 2 s")
    }

    /// Sends the bad record `name` names from wb-hostd's end. For a record
    /// with a descriptor, returns the socket it passed, still open, and
    /// that socket's peer.
    private func sendBad(_ name: String, to host: Int32) throws -> (sent: Int32, peer: Int32)? {
        switch name {
        case "with a descriptor":
            let pair = try makeSocketPair(SOCK_STREAM)
            #expect(
                HandoffChannel(fd: host).send(try ackRecord { _ in }, descriptor: pair.0) == nil)
            return (pair.0, pair.1)
        case "not protobuf":
            sendRecord(host, [0xff, 0xff])
        case "empty":
            sendRecord(host, [])
        case "over 256 bytes":
            sendRecord(host, [UInt8](repeating: 0, count: HandoffChannel.maxRecord + 1))
        case "unknown field":
            // Field 9, varint 1.
            sendRecord(host, try ackRecord { _ in } + [0x48, 0x01])
        default:
            sendRecord(host, try ackRecord(Self.changes[name] ?? { _ in }))
        }
        return nil
    }

    private static let changes: [String: (inout Wraithbox_Vmd_V1_HandoffAck) -> Void] = [
        "generation 0": { $0.generation = 0 },
        "request 0": { $0.requestID = 0 },
        "network with a request": { $0.kind = .network },
        "no kind": { $0.kind = .unspecified },
        "unknown kind": { $0.kind = .UNRECOGNIZED(7) },
        "other request": { $0.requestID = 8 },
        "other generation": { $0.generation = 2 },
        "network not kept": {
            $0.kind = .network
            $0.requestID = 0
        },
    ]

    @Test(
        "when wb-hostd closes its end, reading stops once and the kept copy closes at the deadline")
    func channelEnded() async throws {
        let fixture = try ServiceFixture(handoffDeadline: .milliseconds(300))
        try await fixture.supervisor.start(
            generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC, provisioning: false)
        #expect(fixture.keeper.keptCount == 1)
        // A record for nothing kept wakes the read source, and the close
        // comes before the keeper drains it: darwin doesn't wake a read
        // source for the close of a datagram peer alone. The receive after
        // the record then fails with ECONNRESET.
        sendRecord(fixture.handoff.host, try ackRecord { $0.generation = 2 })
        fixture.closeHostEndOnly()
        #expect(
            try await closeShows {
                fixture.recorded.rules.contains(HandoffAckRule.channelEnded.rawValue)
            }, "no handoff-ack-channel-ended within 2 s")
        #expect(
            try await closeShows { fixture.keeper.keptCount == 0 },
            "the deadline did not close the kept copy within 2 s")
        #expect(fixture.recorded.events("handoff-ack-channel-ended").count == 1)
        #expect(fixture.recorded.events("handoff-ack-timeout").count == 1)
    }

    @Test("closing the keeper closes every kept copy at once and refuses later sends")
    func keeperClosed() async throws {
        let (fixture, machine) = try await runningFixture()
        try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        let connection = try #require(machine.connections.first)
        let received = try #require(receiveHandoff(fixture.handoff.host))
        defer { closeAll(received.descriptors) }
        fixture.keeper.close()
        #expect(connection.closes == 1)
        #expect(fixture.connector.inFlight == 0)
        #expect(fixture.keeper.keptCount == 0)
        let pair = try makeSocketPair(SOCK_STREAM)
        defer { closeAll([pair.0, pair.1]) }
        var closes = 0
        let sent = fixture.keeper.send(
            Wraithbox_Vmd_V1_HandoffMessage(), descriptor: pair.0, close: { closes += 1 })
        #expect(sent == .closed)
        #expect(closes == 0)
    }

    @Test("closeRights closes only the descriptors of well-formed SCM_RIGHTS messages")
    func closeRights() throws {
        let level = SOL_SOCKET
        let rights = SCM_RIGHTS
        // Two descriptors in one message.
        var pairs = try socketPairs(2)
        #expect(HandoffChannel.closeRights(in: cmsg(level, rights, pairs.map(\.0))) == 2)
        #expect(pairs.allSatisfy { streamPeerClosed($0.1) })
        closeAll(pairs.map(\.1))
        // Two messages of one descriptor each.
        pairs = try socketPairs(2)
        let two = cmsg(level, rights, [pairs[0].0]) + cmsg(level, rights, [pairs[1].0])
        #expect(HandoffChannel.closeRights(in: two) == 2)
        #expect(pairs.allSatisfy { streamPeerClosed($0.1) })
        closeAll(pairs.map(\.1))
        // A length under the header's, one past the end, and another type
        // or level close nothing.
        let pair = try makeSocketPair(SOCK_STREAM)
        defer { closeAll([pair.0, pair.1]) }
        var short = cmsg(level, rights, [pair.0])
        setLength(&short, 8)
        #expect(HandoffChannel.closeRights(in: short) == 0)
        var long = cmsg(level, rights, [pair.0])
        setLength(&long, UInt32(long.count + 1))
        #expect(HandoffChannel.closeRights(in: long) == 0)
        #expect(HandoffChannel.closeRights(in: cmsg(level, SCM_TIMESTAMP, [pair.0])) == 0)
        #expect(HandoffChannel.closeRights(in: cmsg(IPPROTO_IP, rights, [pair.0])) == 0)
        #expect(!streamPeerClosed(pair.1))
    }

    @Test("the channel's checks: size, truncation, descriptors and control data cut off")
    func decide() {
        #expect(HandoffChannel.decide([1], flags: 0, descriptors: 0) == .record([1]))
        #expect(HandoffChannel.decide([], flags: 0, descriptors: 0) == .refused("empty record"))
        #expect(
            HandoffChannel.decide([1], flags: MSG_TRUNC, descriptors: 0)
                == .refused("record over 256 bytes"))
        #expect(
            HandoffChannel.decide([1], flags: 0, descriptors: 2)
                == .refused("the record carried 2 descriptors"))
        #expect(
            HandoffChannel.decide([1], flags: MSG_CTRUNC, descriptors: 0)
                == .ended("control data truncated"))
    }
}

/// One control message of `level` and `type` holding `fds`, laid out as
/// darwin does: a 12-byte cmsghdr, then the descriptors.
func cmsg(_ level: Int32, _ type: Int32, _ fds: [Int32]) -> [UInt8] {
    var header = cmsghdr(
        cmsg_len: socklen_t(MemoryLayout<cmsghdr>.size + 4 * fds.count), cmsg_level: level,
        cmsg_type: type)
    var bytes = withUnsafeBytes(of: &header) { Array($0) }
    for var fd in fds {
        bytes += withUnsafeBytes(of: &fd) { Array($0) }
    }
    return bytes
}

/// Sets the cmsg_len of the control message at the start of `bytes`.
func setLength(_ bytes: inout [UInt8], _ length: UInt32) {
    withUnsafeBytes(of: length) { bytes.replaceSubrange(0..<4, with: $0) }
}

/// `count` stream socketpairs.
func socketPairs(_ count: Int) throws -> [(Int32, Int32)] {
    try (0..<count).map { _ in try makeSocketPair(SOCK_STREAM) }
}

/// Whether the peer of the stream socket `fd` is closed: a peek reads end
/// of file. It doesn't write, because a send to a closed stream peer
/// raises SIGPIPE, and SO_NOSIGPIPE can't be set once the peer is gone.
func streamPeerClosed(_ fd: Int32) -> Bool {
    var byte: UInt8 = 0
    return recv(fd, &byte, 1, MSG_PEEK | MSG_DONTWAIT) == 0
}
