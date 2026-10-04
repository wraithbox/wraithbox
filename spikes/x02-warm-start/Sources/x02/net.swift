// X02-warm-start: host side of the file-handle network attachment.
// Throwaway. Records every frame from the guest, answers neighbor
// solicitations for fe80::1, and sends ICMPv6 echo requests to ff02::1 so
// the guest kernel answers on the NIC without any code in the guest.
import Darwin
import Foundation

let hostMAC: [UInt8] = [0x02, 0x58, 0x02, 0x00, 0x00, 0x01]
let hostIP: [UInt8] = [0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01]
let allNodes: [UInt8] = [0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01]
let echoID: UInt16 = 0x5802

struct Frame {
    let t: Double
    let bytes: [UInt8]
    var etherType: UInt16 { bytes.count >= 14 ? UInt16(bytes[12]) << 8 | UInt16(bytes[13]) : 0 }
    /// ICMPv6 type if this is an IPv6 frame without extension headers carrying ICMPv6.
    var icmp6Type: UInt8? {
        guard etherType == 0x86dd, bytes.count >= 14 + 40 + 4, bytes[14 + 6] == 58 else { return nil }
        return bytes[54]
    }
    /// UDP destination port for IPv4 or IPv6 (no extension headers).
    var udpDstPort: UInt16? {
        if etherType == 0x0800, bytes.count >= 14 + 20 + 8, bytes[14 + 9] == 17 {
            let ihl = Int(bytes[14] & 0x0f) * 4
            guard bytes.count >= 14 + ihl + 4 else { return nil }
            return UInt16(bytes[14 + ihl + 2]) << 8 | UInt16(bytes[14 + ihl + 3])
        }
        if etherType == 0x86dd, bytes.count >= 14 + 40 + 8, bytes[14 + 6] == 17 {
            return UInt16(bytes[56]) << 8 | UInt16(bytes[57])
        }
        return nil
    }
    var summary: String {
        if let ty = icmp6Type { return "icmp6/\(ty)" }
        if let p = udpDstPort { return (etherType == 0x0800 ? "udp4/" : "udp6/") + "\(p)" }
        return String(format: "eth/%04x", etherType)
    }
}

func checksum(_ data: [UInt8]) -> UInt16 {
    var sum: UInt32 = 0
    var i = 0
    while i + 1 < data.count { sum += UInt32(data[i]) << 8 | UInt32(data[i + 1]); i += 2 }
    if i < data.count { sum += UInt32(data[i]) << 8 }
    while sum >> 16 != 0 { sum = (sum & 0xffff) + (sum >> 16) }
    return ~UInt16(sum)
}

func ipv6Frame(dstMAC: [UInt8], src: [UInt8], dst: [UInt8], icmp: [UInt8]) -> [UInt8] {
    var msg = icmp
    let pseudo = src + dst + [0, 0, UInt8(msg.count >> 8), UInt8(msg.count & 0xff), 0, 0, 0, 58] + msg
    let c = checksum(pseudo)
    msg[2] = UInt8(c >> 8)
    msg[3] = UInt8(c & 0xff)
    var f = dstMAC + hostMAC + [0x86, 0xdd]
    f += [0x60, 0, 0, 0, UInt8(msg.count >> 8), UInt8(msg.count & 0xff), 58, 255]
    f += src + dst + msg
    return f
}

func echoRequest(seq: UInt16) -> [UInt8] {
    let payload = Array("x02-echo-\(seq)".utf8)
    let icmp: [UInt8] = [128, 0, 0, 0, UInt8(echoID >> 8), UInt8(echoID & 0xff), UInt8(seq >> 8), UInt8(seq & 0xff)] + payload
    return ipv6Frame(dstMAC: [0x33, 0x33, 0, 0, 0, 1], src: hostIP, dst: allNodes, icmp: icmp)
}

/// Neighbor advertisement for fe80::1 in answer to a solicitation, or nil.
func neighborAdvert(for f: Frame) -> [UInt8]? {
    guard f.icmp6Type == 135, f.bytes.count >= 54 + 24 else { return nil }
    let target = Array(f.bytes[62..<78])
    guard target == hostIP else { return nil }
    let srcIP = Array(f.bytes[22..<38])
    if srcIP.allSatisfy({ $0 == 0 }) { return nil }  // DAD probe
    let srcMAC = Array(f.bytes[6..<12])
    let icmp: [UInt8] = [136, 0, 0, 0, 0x60, 0, 0, 0] + hostIP + [2, 1] + hostMAC
    return ipv6Frame(dstMAC: srcMAC, src: hostIP, dst: srcIP, icmp: icmp)
}

/// Host end of a SOCK_DGRAM socketpair; a reader thread records frames.
final class NetEndpoint {
    let hostFD: Int32
    let guestHandle: FileHandle
    private let lock = NSLock()
    private var frames: [Frame] = []
    private var stopped = false

    init() {
        var fds: [Int32] = [0, 0]
        precondition(socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds) == 0)
        var sz: Int32 = 1 << 20
        for fd in fds {
            setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &sz, socklen_t(MemoryLayout<Int32>.size))
            setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &sz, socklen_t(MemoryLayout<Int32>.size))
        }
        hostFD = fds[0]
        guestHandle = FileHandle(fileDescriptor: fds[1], closeOnDealloc: true)
        let fd = hostFD
        Thread.detachNewThread { [weak self] in
            var buf = [UInt8](repeating: 0, count: 65536)
            while true {
                let n = read(fd, &buf, buf.count)
                if n <= 0 { return }
                guard let self else { return }
                let f = Frame(t: now(), bytes: Array(buf[0..<n]))
                if let na = neighborAdvert(for: f) { _ = self.send(na) }
                self.lock.lock()
                self.frames.append(f)
                let s = self.stopped
                self.lock.unlock()
                if s { return }
            }
        }
    }

    @discardableResult
    func send(_ frame: [UInt8]) -> Bool {
        frame.withUnsafeBytes { write(hostFD, $0.baseAddress, $0.count) } == frame.count
    }

    func snapshot() -> [Frame] { lock.lock(); defer { lock.unlock() }; return frames }

    func firstFrame(after t: Double, where pred: (Frame) -> Bool = { _ in true }) -> Frame? {
        lock.lock(); defer { lock.unlock() }
        return frames.first(where: { $0.t >= t && pred($0) })
    }

    /// Time of the echo reply for seq, if any.
    func echoReply(seq: UInt16) -> Double? {
        firstFrame(after: 0) { f in
            f.icmp6Type == 129 && f.bytes.count >= 62
                && f.bytes[58] == UInt8(echoID >> 8) && f.bytes[59] == UInt8(echoID & 0xff)
                && f.bytes[60] == UInt8(seq >> 8) && f.bytes[61] == UInt8(seq & 0xff)
        }?.t
    }

    func close() {
        lock.lock(); stopped = true; lock.unlock()
        Darwin.shutdown(hostFD, SHUT_RDWR)
        Darwin.close(hostFD)
    }
}
