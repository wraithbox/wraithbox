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
            generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
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
            generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
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
        try sendBad(name, to: fixture.handoff.host)
        #expect(
            try await closeShows { fixture.recorded.rules.contains(rule.rawValue) },
            "no \(rule.rawValue) within 2 s")
        #expect(connection.closes == 0)
        #expect(fixture.keeper.keptCount == 1)
        // The wait goes on: the right acknowledgment still closes the copy.
        try sendAck(fixture.handoff.host, for: received)
        #expect(
            try await closeShows { connection.closes == 1 },
            "the acknowledgment after the refusal did not close the copy within 2 s")
    }

    /// Sends the bad record `name` names from wb-hostd's end.
    private func sendBad(_ name: String, to host: Int32) throws {
        switch name {
        case "with a descriptor":
            let pair = try makeSocketPair(SOCK_STREAM)
            defer { closeAll([pair.0, pair.1]) }
            #expect(
                HandoffChannel(fd: host).send(try ackRecord { _ in }, descriptor: pair.0) == nil)
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
