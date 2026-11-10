// The hand-off channel's socket calls (S04-02-vm-provider, "Hand-off on
// macOS"). wb-vmd sends a HandoffMessage record with one descriptor for
// each descriptor it hands to wb-hostd, and reads back the HandoffAck
// records wb-hostd answers with (S07-05-stream-hand-off, "Transport").
// HandoffKeeper decides what a received record means.
import Darwin

/// The hand-off channel: an AF_UNIX SOCK_DGRAM socket that wb-hostd
/// passed as fd 4 (S04-11-vmd-start, "How `wb-vmd` is started").
public struct HandoffChannel: Sendable {
    /// The largest record either side sends, as the comments on
    /// HandoffMessage and HandoffAck in proto/wraithbox/vmd/v1 state.
    public static let maxRecord = 256

    /// Room for the control data of every descriptor one message can
    /// carry on darwin, 254, and four times that, as
    /// internal/platform/fdchan has: darwin installs every descriptor of
    /// a message even when the control buffer cuts some off, and those
    /// can't be closed.
    static let controlRoom = 4 * 254 * MemoryLayout<Int32>.size + 64

    public let fd: Int32

    public init(fd: Int32) {
        self.fd = fd
    }

    /// Sends `record` as one datagram with `descriptor` attached by
    /// SCM_RIGHTS. It makes one attempt and doesn't wait: a peer whose
    /// buffer is full fails the send with ENOBUFS or EAGAIN instead of
    /// blocking wb-vmd. Returns the errno of a failed send, or nil.
    public func send(_ record: [UInt8], descriptor: Int32) -> Int32? {
        // CMSG_LEN(sizeof(int)) and CMSG_SPACE(sizeof(int)): the header,
        // aligned to 4 bytes on Darwin, then one descriptor.
        let dataOffset = Self.align(MemoryLayout<cmsghdr>.size)
        var control = [UInt8](
            repeating: 0, count: dataOffset + Self.align(MemoryLayout<Int32>.size))
        control.withUnsafeMutableBytes { raw in
            let header = cmsghdr(
                cmsg_len: socklen_t(dataOffset + MemoryLayout<Int32>.size), cmsg_level: SOL_SOCKET,
                cmsg_type: SCM_RIGHTS)
            raw.storeBytes(of: header, toByteOffset: 0, as: cmsghdr.self)
            raw.storeBytes(of: descriptor, toByteOffset: dataOffset, as: Int32.self)
        }
        var bytes = record
        while true {
            let sent = bytes.withUnsafeMutableBytes { data in
                control.withUnsafeMutableBytes { ctl in
                    var iov = iovec(iov_base: data.baseAddress, iov_len: data.count)
                    return withUnsafeMutablePointer(to: &iov) { iovPointer in
                        var message = msghdr(
                            msg_name: nil, msg_namelen: 0, msg_iov: iovPointer, msg_iovlen: 1,
                            msg_control: ctl.baseAddress, msg_controllen: socklen_t(ctl.count),
                            msg_flags: 0)
                        return sendmsg(fd, &message, MSG_DONTWAIT)
                    }
                }
            }
            if sent == record.count {
                return nil
            }
            if sent < 0 && errno == EINTR {
                continue
            }
            return sent < 0 ? errno : EMSGSIZE
        }
    }

    /// What one receive got.
    public enum Received: Equatable, Sendable {
        /// A record of 1 to maxRecord bytes with no descriptor.
        case record([UInt8])
        /// A record refused before it is decoded: empty, over maxRecord
        /// bytes or truncated, or one that carried descriptors, which
        /// are closed.
        case refused(String)
        /// Nothing is queued.
        case none
        /// The channel can't be read any more: a receive error, or
        /// control data cut off, which may have left descriptors no one
        /// can name.
        case ended(String)
    }

    /// Receives one record without waiting. A record over maxRecord bytes
    /// is read into a buffer one byte larger, so it shows as too long
    /// rather than cut to fit.
    public func receive() -> Received {
        var data = [UInt8](repeating: 0, count: Self.maxRecord + 1)
        var control = [UInt8](repeating: 0, count: Self.controlRoom)
        var controlLength = 0
        var flags: Int32 = 0
        var count = -1
        repeat {
            count = data.withUnsafeMutableBytes { dataBytes in
                control.withUnsafeMutableBytes { controlBytes in
                    var iov = iovec(iov_base: dataBytes.baseAddress, iov_len: dataBytes.count)
                    return withUnsafeMutablePointer(to: &iov) { iovPointer in
                        var message = msghdr(
                            msg_name: nil, msg_namelen: 0, msg_iov: iovPointer, msg_iovlen: 1,
                            msg_control: controlBytes.baseAddress,
                            msg_controllen: socklen_t(controlBytes.count), msg_flags: 0)
                        let received = recvmsg(fd, &message, MSG_DONTWAIT)
                        controlLength = Int(message.msg_controllen)
                        flags = message.msg_flags
                        return received
                    }
                }
            }
        } while count < 0 && errno == EINTR
        if count < 0 {
            return errno == EAGAIN ? .none : .ended("recvmsg: \(String(cString: strerror(errno)))")
        }
        let closed = Self.closeRights(in: Array(control.prefix(min(controlLength, control.count))))
        return Self.decide(
            Array(data.prefix(count)), flags: flags, descriptors: closed)
    }

    /// Whether wb-hostd has closed its end: darwin disconnects the
    /// survivor of a datagram socketpair, as internal/platform/fdchan
    /// checks too.
    public func peerClosed() -> Bool {
        var address = sockaddr_storage()
        var length = socklen_t(MemoryLayout<sockaddr_storage>.size)
        let result = withUnsafeMutablePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                getpeername(fd, $0, &length)
            }
        }
        return result != 0 && errno == ENOTCONN
    }

    /// Decides a received record by its size, flags and the descriptors
    /// that came with it, which are closed already.
    static func decide(_ record: [UInt8], flags: Int32, descriptors: Int) -> Received {
        if flags & MSG_CTRUNC != 0 {
            return .ended("control data truncated")
        }
        if descriptors > 0 {
            return .refused("the record carried \(descriptors) descriptors")
        }
        if flags & MSG_TRUNC != 0 || record.count > maxRecord {
            return .refused("record over \(maxRecord) bytes")
        }
        if record.isEmpty {
            return .refused("empty record")
        }
        return .record(record)
    }

    /// Closes the descriptors of every SCM_RIGHTS message in control
    /// data, and returns how many there were.
    static func closeRights(in control: [UInt8]) -> Int {
        var closed = 0
        var offset = 0
        let header = align(MemoryLayout<cmsghdr>.size)
        while offset + MemoryLayout<cmsghdr>.size <= control.count {
            let message = control.withUnsafeBytes {
                $0.loadUnaligned(fromByteOffset: offset, as: cmsghdr.self)
            }
            let length = Int(message.cmsg_len)
            guard length >= header, offset + length <= control.count else {
                break
            }
            if message.cmsg_level == SOL_SOCKET, message.cmsg_type == SCM_RIGHTS {
                let size = MemoryLayout<Int32>.size
                for at in stride(from: offset + header, to: offset + length - size + 1, by: size) {
                    Darwin.close(
                        control.withUnsafeBytes {
                            $0.loadUnaligned(fromByteOffset: at, as: Int32.self)
                        })
                    closed += 1
                }
            }
            offset += align(length)
        }
        return closed
    }

    private static func align(_ length: Int) -> Int {
        (length + 3) & ~3
    }
}
