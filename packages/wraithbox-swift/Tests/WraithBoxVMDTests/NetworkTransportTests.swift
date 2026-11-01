// C11-host-daemons, "Packet transport": the socketpair's type and buffer
// sizes, the MTU and MAC address on the attachment, the KIND_NETWORK
// record wb-vmd sends before the VM starts, and the MAC addresses Start
// refuses with vmd-mac-invalid (S07-02-packet-path, "Packet transport",
// "MTU").
import Darwin
import Foundation
import GRPCCore
import Testing
import Virtualization
import WraithBoxRPC

@testable import WraithBoxVMD

/// An integer socket option of `fd`, or nil when getsockopt fails.
func socketOption(_ fd: Int32, _ option: Int32) -> Int32? {
    var value: Int32 = 0
    var length = socklen_t(MemoryLayout<Int32>.size)
    guard getsockopt(fd, SOL_SOCKET, option, &value, &length) == 0 else {
        return nil
    }
    return value
}

/// The address family of the socket `fd`.
func socketFamily(_ fd: Int32) -> Int32? {
    var address = sockaddr_storage()
    var length = socklen_t(MemoryLayout<sockaddr_storage>.size)
    let result = withUnsafeMutablePointer(to: &address) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &length) }
    }
    return result == 0 ? Int32(address.ss_family) : nil
}

@Suite("Packet transport")
struct PacketTransportTests {
    @Test("both ends are AF_UNIX datagram sockets, close-on-exec, with SO_RCVBUF twice SO_SNDBUF")
    func socketpair() throws {
        let transport = try PacketTransport()
        defer { transport.close() }
        for fd in [transport.vmEnd, transport.hostEnd] {
            #expect(socketFamily(fd) == AF_UNIX)
            #expect(socketOption(fd, SO_TYPE) == SOCK_DGRAM)
            #expect(fcntl(fd, F_GETFD) & FD_CLOEXEC != 0)
            let send = try #require(socketOption(fd, SO_SNDBUF))
            let receive = try #require(socketOption(fd, SO_RCVBUF))
            #expect(send == PacketTransport.sendBuffer)
            #expect(receive >= 2 * send)
            // The largest frame fits in one datagram (S07-02-packet-path, "MTU").
            #expect(Int(send) > PacketTransport.mtu + 14)
        }
    }

    @Test("closing closes both of wb-vmd's descriptors")
    func closes() throws {
        let transport = try PacketTransport()
        let fds = [transport.vmEnd, transport.hostEnd]
        transport.closeHostEnd()
        #expect(fcntl(fds[1], F_GETFD) == -1)
        #expect(fcntl(fds[0], F_GETFD) != -1)
        transport.close()
        #expect(fcntl(fds[0], F_GETFD) == -1)
    }

    @Test("the attachment has MTU 65535, the MAC address, and its own copy of the VM end")
    @MainActor
    func attachment() throws {
        let transport = try PacketTransport()
        defer { transport.close() }
        let mac = try MACAddress(validating: testMAC)
        let device = try VirtualizationConfiguration.networkDevice(
            descriptor: transport.vmEnd, mac: mac)
        let attachment = try #require(device.attachment as? VZFileHandleNetworkDeviceAttachment)
        #expect(attachment.maximumTransmissionUnit == 65535)
        #expect(device.macAddress.string == "02:57:42:00:00:01")
        let copy = attachment.fileHandle.fileDescriptor
        #expect(copy != transport.vmEnd)
        #expect(fcntl(copy, F_GETFD) & FD_CLOEXEC != 0)
    }
}

@Suite("MAC address")
struct MACAddressTests {
    @Test(
        "anything but a locally administered unicast address is refused with vmd-mac-invalid",
        arguments: [
            [0x03, 0x57, 0x42, 0x00, 0x00, 0x01],  // multicast, locally administered
            [0x01, 0x00, 0x5E, 0x00, 0x00, 0x01],  // multicast
            [0x00, 0x1C, 0x42, 0x00, 0x00, 0x01],  // universally administered
            [0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF],  // broadcast
            [0x02, 0x57, 0x42, 0x00, 0x00],  // five bytes
            [],
        ] as [[UInt8]])
    func refused(bytes: [UInt8]) {
        #expect {
            _ = try MACAddress(validating: bytes)
        } throws: { error in
            (error as? VMRefusal)?.rule == VMRule.macInvalid.rawValue
        }
    }

    @Test("a locally administered unicast address is accepted")
    func accepted() throws {
        #expect(try MACAddress(validating: testMAC).description == "02:57:42:00:00:01")
    }

    @Test(
        "Start refuses a bad MAC address with vmd-mac-invalid before it builds a VM or sends a record",
        arguments: [
            [0x03, 0x57, 0x42, 0x00, 0x00, 0x01], [0x00, 0x1C, 0x42, 0x00, 0x00, 0x01], [],
        ] as [[UInt8]])
    func startRefused(bytes: [UInt8]) async throws {
        let fixture = try await ServiceFixture()
        let (code, reason) = try await fixture.withClient { client in
            await refusal { _ = try await client.start(startRequest(generation: 1, mac: bytes)) }
        }
        #expect(code == .invalidArgument)
        #expect(reason == VMRule.macInvalid.rawValue)
        #expect(await fixture.engine.machines.isEmpty)
        #expect(receiveHandoff(fixture.handoff.host) == nil)
        #expect(fixture.recorded.rules.contains(VMRule.macInvalid.rawValue))
    }
}

@Suite("Network hand-off")
@MainActor
struct NetworkHandoffTests {
    @Test("Start sends one KIND_NETWORK record with the host end before the VM starts")
    func record() async throws {
        let fixture = try ServiceFixture()
        fixture.engine.holdStart = true
        let supervisor = fixture.supervisor
        let start = Task {
            try await supervisor.start(
                generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
        }
        let machine = try await heldMachine(fixture.engine)
        // The VM hasn't started, and the record is there.
        let received = try #require(receiveHandoff(fixture.handoff.host))
        defer { closeAll(received.descriptors) }
        machine.releaseStart()
        try await start.value

        let message = try Wraithbox_Vmd_V1_HandoffMessage(serializedBytes: received.record)
        #expect(message.kind == .network)
        #expect(message.generation == 1)
        #expect(message.requestID == 0)
        #expect(message.direction == .unspecified)
        #expect(message.sourcePort == 0 && message.destinationPort == 0)
        #expect(received.descriptors.count == 1)
        let host = try #require(received.descriptors.first)
        #expect(socketFamily(host) == AF_UNIX)
        #expect(socketOption(host, SO_TYPE) == SOCK_DGRAM)

        // The host end leads to the VM end the engine keeps.
        let vmEnd = try #require(fixture.engine.networkEnds.first)
        #expect(send(host, "frame", 5, 0) == 5)
        var buffer = [UInt8](repeating: 0, count: 16)
        #expect(recv(vmEnd, &buffer, buffer.count, MSG_DONTWAIT) == 5)
        #expect(receiveHandoff(fixture.handoff.host) == nil)
        let passed = try #require(fixture.recorded.events("network-transport-passed").first)
        #expect(passed["mac_address"] as? String == "02:57:42:00:00:01")
    }

    @Test("a record that can't be sent refuses the start with handoff-send-failed")
    func sendFailed() async throws {
        let fixture = try ServiceFixture()
        fixture.closeHostEnd()
        await #expect {
            try await fixture.supervisor.start(
                generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
        } throws: { error in
            (error as? VMRefusal)?.rule == VMRule.handoffSendFailed.rawValue
        }
        #expect(fixture.supervisor.status.state == .error)
        #expect(fixture.supervisor.status.generation == 1)
    }

    /// The fake VM once its start is held.
    private func heldMachine(_ engine: FakeEngine) async throws -> FakeVM {
        for _ in 0..<200 {
            if let machine = engine.machines.first, machine.isHeld {
                return machine
            }
            try await Task.sleep(for: .milliseconds(5))
        }
        throw POSIXError(.ETIMEDOUT)
    }
}
