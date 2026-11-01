// What the needs-vm boot test reads on the packet transport: the frames
// the guest sends, and whether one is a DHCP client message from the VM's
// MAC address (C11-host-daemons, "Packet transport").
import Darwin
import Foundation
import Testing

/// The DHCP message type (option 53) of `frame` when it is an Ethernet
/// frame from `mac` that carries a DHCP client message over IPv4 UDP from
/// port 68 to port 67, or nil when it is anything else.
func dhcpClientMessageType(_ frame: [UInt8], from mac: [UInt8]) -> UInt8? {
    // Ethernet: destination, source, EtherType 0x0800.
    guard frame.count >= 14 + 20, Array(frame[6..<12]) == mac, frame[12] == 0x08,
        frame[13] == 0x00
    else {
        return nil
    }
    let ip = 14
    let headerLength = Int(frame[ip] & 0x0F) * 4
    guard frame[ip] >> 4 == 4, headerLength >= 20, frame[ip + 9] == 17 else {
        return nil
    }
    let udp = ip + headerLength
    guard frame.count >= udp + 8, port(frame, udp) == 68, port(frame, udp + 2) == 67 else {
        return nil
    }
    return dhcpMessageType(Array(frame[(udp + 8)...]), mac: mac)
}

/// Option 53 of a BOOTREQUEST whose client hardware address is `mac`.
private func dhcpMessageType(_ dhcp: [UInt8], mac: [UInt8]) -> UInt8? {
    let cookie: [UInt8] = [0x63, 0x82, 0x53, 0x63]
    guard dhcp.count >= 240, dhcp[0] == 1, Array(dhcp[28..<34]) == mac,
        Array(dhcp[236..<240]) == cookie
    else {
        return nil
    }
    var at = 240
    while at < dhcp.count {
        let code = dhcp[at]
        if code == 255 {
            return nil
        }
        if code == 0 {
            at += 1
            continue
        }
        guard at + 1 < dhcp.count else {
            return nil
        }
        let length = Int(dhcp[at + 1])
        if code == 53, length == 1, at + 2 < dhcp.count {
            return dhcp[at + 2]
        }
        at += 2 + length
    }
    return nil
}

private func port(_ bytes: [UInt8], _ at: Int) -> Int {
    Int(bytes[at]) << 8 | Int(bytes[at + 1])
}

/// Reads frames from the host end of the packet transport until one is a
/// DHCPDISCOVER from `mac`, or `seconds` pass. It returns whether one came,
/// and how many frames it read.
func waitForDHCPDiscover(_ fd: Int32, mac: [UInt8], seconds: Int) -> (found: Bool, frames: Int) {
    var timeout = timeval(tv_sec: 1, tv_usec: 0)
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
    let deadline = Date().addingTimeInterval(TimeInterval(seconds))
    var buffer = [UInt8](repeating: 0, count: 65535 + 14)
    var frames = 0
    while Date() < deadline {
        let count = buffer.withUnsafeMutableBytes { recv(fd, $0.baseAddress, $0.count, 0) }
        guard count > 0 else {
            continue
        }
        frames += 1
        if dhcpClientMessageType(Array(buffer.prefix(count)), from: mac) == 1 {
            return (true, frames)
        }
    }
    return (false, frames)
}

/// Waits up to `seconds` for a record on the test's end of the hand-off
/// channel.
func waitForHandoff(_ fd: Int32, seconds: Int) async throws -> ReceivedHandoff? {
    for _ in 0..<(seconds * 20) {
        if let received = receiveHandoff(fd) {
            return received
        }
        try await Task.sleep(for: .milliseconds(50))
    }
    return nil
}

/// A DHCP client message of `type` from `mac`, as a guest sends it.
func dhcpFrame(type: UInt8, mac: [UInt8], sourcePort: UInt8 = 68) -> [UInt8] {
    var dhcp = [UInt8](repeating: 0, count: 240)
    dhcp[0] = 1
    dhcp[1] = 1
    dhcp[2] = 6
    dhcp.replaceSubrange(28..<34, with: mac)
    dhcp.replaceSubrange(236..<240, with: [0x63, 0x82, 0x53, 0x63])
    dhcp += [0, 53, 1, type, 255]
    let udp: [UInt8] = [0, sourcePort, 0, 67, 0, 0, 0, 0] + dhcp
    let ip: [UInt8] = [0x45, 0, 0, 0, 0, 0, 0, 0, 64, 17, 0, 0, 0, 0, 0, 0, 255, 255, 255, 255]
    return [UInt8](repeating: 0xFF, count: 6) + mac + [0x08, 0x00] + ip + udp
}

@Suite("DHCP frames")
struct DHCPFrameTests {
    @Test("a DHCPDISCOVER from the VM's MAC address is recognized")
    func discover() {
        #expect(dhcpClientMessageType(dhcpFrame(type: 1, mac: testMAC), from: testMAC) == 1)
        #expect(dhcpClientMessageType(dhcpFrame(type: 3, mac: testMAC), from: testMAC) == 3)
    }

    @Test("another source, port or a cut frame is not a DHCP client message")
    func others() {
        let other: [UInt8] = [0x02, 0, 0, 0, 0, 9]
        #expect(dhcpClientMessageType(dhcpFrame(type: 1, mac: other), from: testMAC) == nil)
        #expect(
            dhcpClientMessageType(dhcpFrame(type: 1, mac: testMAC, sourcePort: 69), from: testMAC)
                == nil)
        #expect(
            dhcpClientMessageType(
                Array(dhcpFrame(type: 1, mac: testMAC).prefix(200)), from: testMAC)
                == nil)
        #expect(dhcpClientMessageType([], from: testMAC) == nil)
    }
}
