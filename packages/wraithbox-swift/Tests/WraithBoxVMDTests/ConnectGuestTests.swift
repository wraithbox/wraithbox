// C11-host-daemons, "ConnectGuest": with a fake socket device and a real
// hand-off socketpair, each refusal has its rule and sends nothing, a
// connection is sent once with the right fields and wb-vmd's copy is
// closed after, and the connects in flight are capped.
import Darwin
import Foundation
import GRPCCore
import SwiftProtobuf
import Testing
import WraithBoxRPC

@testable import WraithBoxVMD

func connectRequest(generation: UInt64, requestID: UInt64, port: UInt32)
    -> Wraithbox_Vmd_V1_ConnectGuestRequest
{
    var request = Wraithbox_Vmd_V1_ConnectGuestRequest()
    request.generation = generation
    request.requestID = requestID
    request.guestPort = port
    return request
}

/// A fixture whose VM of generation 1 runs.
@MainActor
func runningFixture(connectTimeout: Duration = GuestConnector.defaultTimeout) async throws
    -> (ServiceFixture, FakeVM)
{
    let fixture = try ServiceFixture(connectTimeout: connectTimeout)
    try await fixture.supervisor.start(
        generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
    let machine = try #require(fixture.engine.machines.first)
    // The start's network record comes first. The tests here read only
    // the connection records after it.
    let network = try #require(receiveHandoff(fixture.handoff.host))
    closeAll(network.descriptors)
    return (fixture, machine)
}

@Suite("ConnectGuest")
@MainActor
struct ConnectGuestTests {
    @Test("a connection is sent once, with the request's fields, and wb-vmd's copy is closed")
    func passed() async throws {
        let (fixture, machine) = try await runningFixture()
        try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        let connection = try #require(machine.connections.first)
        // Checked before the record is read, so no received descriptor
        // can have taken the number.
        #expect(connection.closes == 1)
        #expect(fcntl(connection.fileDescriptor, F_GETFD) == -1)
        #expect(fixture.connector.inFlight == 0)

        let received = try #require(receiveHandoff(fixture.handoff.host))
        defer { closeAll(received.descriptors) }
        let message = try Wraithbox_Vmd_V1_HandoffMessage(serializedBytes: received.record)
        #expect(message.kind == .connection)
        #expect(message.requestID == 7)
        #expect(message.generation == 1)
        #expect(message.direction == .hostToGuest)
        #expect(message.sourcePort == 49152)
        #expect(message.destinationPort == 1023)
        #expect(received.descriptors.count == 1)
        // The passed descriptor reaches the guest end.
        let passed = try #require(received.descriptors.first)
        #expect(write(passed, "x", 1) == 1)
        var byte: UInt8 = 0
        #expect(read(connection.guest, &byte, 1) == 1 && byte == UInt8(ascii: "x"))
        #expect(receiveHandoff(fixture.handoff.host) == nil)
        #expect(!fixture.recorded.events("guest-connection-passed").isEmpty)
    }

    @Test(
        "refusals have their rule, reach no connect and send nothing",
        arguments: [
            (UInt64(1), UInt64(7), UInt32(1024), VMRule.guestPortNotReserved),
            (1, 7, UInt32.max, .guestPortNotReserved),
            (1, 0, 1023, .requestID),
            (0, 7, 1023, .generation),
            (2, 7, 1023, .generation),
        ])
    func refused(generation: UInt64, requestID: UInt64, port: UInt32, rule: VMRule) async throws {
        let (fixture, machine) = try await runningFixture()
        await #expect {
            try await fixture.connector.connect(
                generation: generation, requestID: requestID, port: port)
        } throws: { error in
            (error as? VMRefusal)?.rule == rule.rawValue
        }
        #expect(machine.connects.isEmpty)
        #expect(receiveHandoff(fixture.handoff.host) == nil)
    }

    @Test("a VM that isn't running is refused with vm-not-running")
    func notRunning() async throws {
        let (fixture, machine) = try await runningFixture()
        try await fixture.supervisor.stop(generation: 1, mode: .force)
        await #expect(throws: VMRefusal(.notRunning, .failedPrecondition, "the VM is stopped")) {
            try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        }
        #expect(machine.connects.isEmpty)
    }

    @Test("a failed connect is refused with vm-connect-failed, sends nothing and frees its slot")
    func failed() async throws {
        let (fixture, machine) = try await runningFixture()
        machine.connectMode = .fail(VMFailure(description: "connection refused"))
        await #expect(
            throws: VMRefusal(.connectFailed, .failedPrecondition, "connection refused")
        ) {
            try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        }
        #expect(fixture.connector.inFlight == 0)
        #expect(receiveHandoff(fixture.handoff.host) == nil)
    }

    @Test("connects in flight are capped with connect-cap, and released ones are all passed")
    func cap() async throws {
        let (fixture, machine) = try await runningFixture()
        machine.connectMode = .hold
        let connector = fixture.connector
        var tasks: [Task<Void, any Error>] = []
        for id in 1...UInt64(GuestConnector.maxInFlight) {
            tasks.append(
                Task { try await connector.connect(generation: 1, requestID: id, port: 1023) })
        }
        for _ in 0..<200 where machine.heldCount < GuestConnector.maxInFlight {
            try await Task.sleep(for: .milliseconds(5))
        }
        #expect(machine.heldCount == GuestConnector.maxInFlight)
        await #expect(
            throws: VMRefusal(.connectCap, .resourceExhausted, "8 connects are in flight")
        ) {
            try await connector.connect(generation: 1, requestID: 99, port: 1023)
        }
        #expect(fixture.recorded.rules.isEmpty)
        machine.releaseConnects()
        for task in tasks {
            try await task.value
        }
        #expect(connector.inFlight == 0)
        var records = 0
        while let received = receiveHandoff(fixture.handoff.host) {
            closeAll(received.descriptors)
            records += 1
        }
        #expect(records == GuestConnector.maxInFlight)
    }

    @Test("a connect that times out is refused, holds its slot until it completes, and is closed")
    func timeout() async throws {
        let (fixture, machine) = try await runningFixture(connectTimeout: .milliseconds(50))
        machine.connectMode = .hold
        await #expect {
            try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        } throws: { error in
            (error as? VMRefusal)?.rule == VMRule.connectTimeout.rawValue
        }
        #expect(fixture.connector.inFlight == 1)
        machine.releaseConnects()
        #expect(fixture.connector.inFlight == 0)
        #expect(machine.connections.first?.closes == 1)
        #expect(receiveHandoff(fixture.handoff.host) == nil)
    }

    @Test("a send that fails is refused with handoff-send-failed, and the connection is closed")
    func sendFailed() async throws {
        let (fixture, machine) = try await runningFixture()
        // wb-hostd's end is gone.
        fixture.closeHostEnd()
        await #expect {
            try await fixture.connector.connect(generation: 1, requestID: 7, port: 1023)
        } throws: { error in
            (error as? VMRefusal)?.rule == VMRule.handoffSendFailed.rawValue
        }
        #expect(machine.connections.first?.closes == 1)
        #expect(fixture.connector.inFlight == 0)
    }

    @Test("over gRPC, a refusal comes back with its rule as the ErrorInfo reason, and is logged")
    nonisolated func overGRPC() async throws {
        let (fixture, _) = try await runningFixture()
        let refused = await refusal {
            try await fixture.withClient { client in
                _ = try await client.connectGuest(
                    connectRequest(generation: 1, requestID: 7, port: 1024))
            }
        }
        #expect(refused == (.invalidArgument, "guest-port-not-reserved"))
        #expect(fixture.recorded.rules == ["guest-port-not-reserved"])
        try await fixture.withClient { client in
            _ = try await client.connectGuest(
                connectRequest(generation: 1, requestID: 8, port: 1023))
        }
        let received = try #require(receiveHandoff(fixture.handoff.host))
        closeAll(received.descriptors)
        #expect(received.descriptors.count == 1)
    }
}
