// The VM's packet transport and its MAC address (S07-02-packet-path,
// "Packet transport"). At each start wb-vmd makes an AF_UNIX SOCK_DGRAM
// socketpair: one end goes to the VM's file-handle network attachment,
// and the other to wb-hostd in one KIND_NETWORK hand-off record, which
// wb-hostd later gives to the VM's wb-netd. wb-vmd never reads or writes
// a frame, so guest bytes stay out of its code (SEC12-least-privilege).
import Darwin

/// The MAC address of the VM's one network device. wb-hostd makes it once
/// per slot and keeps it (S06-06-devices-and-warm-start, "Disks and
/// identity"). wb-vmd takes only a locally administered unicast address,
/// so the VM never claims a vendor's address or a group address.
public struct MACAddress: Sendable, Equatable, CustomStringConvertible {
    public let bytes: [UInt8]

    /// Checks `bytes` from a StartRequest, and refuses anything but six
    /// bytes of a locally administered unicast address with
    /// `vmd-mac-invalid`.
    public init(validating bytes: [UInt8]) throws(VMRefusal) {
        guard bytes.count == 6 else {
            throw VMRefusal(
                .macInvalid, .invalidArgument, "mac_address is \(bytes.count) bytes, not 6")
        }
        // The first octet's lowest bit marks a group (multicast) address,
        // and the next one a locally administered address (IEEE 802).
        guard bytes[0] & 0x01 == 0 else {
            throw VMRefusal(
                .macInvalid, .invalidArgument, "mac_address \(Self.text(bytes)) is multicast")
        }
        guard bytes[0] & 0x02 != 0 else {
            throw VMRefusal(
                .macInvalid, .invalidArgument,
                "mac_address \(Self.text(bytes)) is not locally administered")
        }
        self.bytes = bytes
    }

    public var description: String {
        Self.text(bytes)
    }

    private static func text(_ bytes: [UInt8]) -> String {
        bytes.map { byte in
            let hex = String(byte, radix: 16)
            return hex.count == 1 ? "0" + hex : hex
        }.joined(separator: ":")
    }
}

/// One VM's packet transport: a datagram socketpair. wb-vmd keeps neither
/// end: the engine takes a copy of `vmEnd` for the attachment, and
/// `hostEnd` goes to wb-hostd. Both of wb-vmd's descriptors are closed
/// when the value is closed.
public final class PacketTransport {
    /// SO_SNDBUF of both ends. A frame is at most MTU + 14 bytes, and a
    /// datagram socket's send buffer bounds the largest datagram it
    /// sends, so it holds several. A value in code (S07-02-packet-path,
    /// "Packet transport").
    static let sendBuffer: Int32 = 1 << 20
    /// SO_RCVBUF of both ends: at least twice SO_SNDBUF, as Apple's header
    /// for the file-handle attachment asks (S07-02-packet-path, "Packet
    /// transport"). A value in code.
    static let receiveBuffer: Int32 = 4 << 20
    /// The attachment's MTU, the largest the framework accepts
    /// (S07-02-packet-path, "MTU").
    public static let mtu = 65535

    /// The end the VM's network attachment gets a copy of.
    public private(set) var vmEnd: Int32
    /// The end that goes to wb-hostd.
    public private(set) var hostEnd: Int32

    /// Makes the socketpair, close-on-exec, with the buffer sizes set on
    /// both ends. Throws `vm-network-failed` with the errno.
    public init() throws(VMRefusal) {
        var fds: [Int32] = [-1, -1]
        guard socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds) == 0 else {
            throw Self.failure("socketpair")
        }
        vmEnd = fds[0]
        hostEnd = fds[1]
        for fd in fds {
            guard fcntl(fd, F_SETFD, FD_CLOEXEC) == 0,
                Self.setBuffer(fd, SO_SNDBUF, Self.sendBuffer),
                Self.setBuffer(fd, SO_RCVBUF, Self.receiveBuffer)
            else {
                let refusal = Self.failure("setsockopt")
                close()
                throw refusal
            }
        }
    }

    deinit {
        if vmEnd >= 0 {
            Darwin.close(vmEnd)
        }
        if hostEnd >= 0 {
            Darwin.close(hostEnd)
        }
    }

    /// Closes wb-vmd's copy of the host end, once it is sent
    /// (X18-vsock-handoff, condition 4).
    public func closeHostEnd() {
        if hostEnd >= 0 {
            Darwin.close(hostEnd)
            hostEnd = -1
        }
    }

    /// Closes both of wb-vmd's descriptors.
    public func close() {
        closeHostEnd()
        if vmEnd >= 0 {
            Darwin.close(vmEnd)
            vmEnd = -1
        }
    }

    private static func setBuffer(_ fd: Int32, _ option: Int32, _ bytes: Int32) -> Bool {
        var value = bytes
        return setsockopt(fd, SOL_SOCKET, option, &value, socklen_t(MemoryLayout<Int32>.size)) == 0
    }

    private static func failure(_ call: String) -> VMRefusal {
        VMRefusal(
            .networkFailed, .failedPrecondition,
            "\(call): \(String(cString: strerror(errno)))")
    }
}
