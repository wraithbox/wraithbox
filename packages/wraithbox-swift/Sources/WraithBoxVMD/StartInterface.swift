// The start interface of wb-vmd (S04-03-self-sandboxing, "How `wb-vmd`
// is started"): no arguments, and four inherited descriptors. wb-vmd
// checks them after it confines itself, and refuses to run when one is
// missing or of another kind, so it never serves on a descriptor its
// spawner didn't mean.
import Darwin
import Foundation
import WraithBoxVM

/// The descriptors wb-vmd inherits.
public enum InheritedDescriptor: Int32, Sendable, CaseIterable {
    /// The lifeline: a pipe or socket wb-vmd only reads. End of file means
    /// its spawner is gone.
    case lifeline = 0
    /// The log pipe.
    case log = 2
    /// A listening AF_UNIX stream socket for gRPC, bound by the spawner in
    /// `<data>/run/`.
    case grpc = 3
    /// The hand-off socket: AF_UNIX, SOCK_DGRAM (S04-02-vm-provider,
    /// "Hand-off on macOS"). Held open here; ConnectGuest uses it.
    case handoff = 4

    var name: String {
        switch self {
        case .lifeline: "lifeline"
        case .log: "log"
        case .grpc: "gRPC"
        case .handoff: "hand-off"
        }
    }
}

/// The checks of the start interface.
public enum StartInterface {
    /// Refuses a macOS older than the oldest v1 host. The package targets
    /// an older macOS than it runs on (HostRequirement), so this check,
    /// not the loader, keeps wb-vmd off such a host.
    public static func checkHost(_ version: OperatingSystemVersion) throws(StartupRefusal) {
        guard HostRequirement.supports(version) else {
            throw StartupRefusal(
                .hostUnsupported,
                "macOS \(HostRequirement.describe(version)) is older than macOS "
                    + "\(HostRequirement.minimumMacOS)")
        }
    }

    /// Refuses any command-line argument: wb-launcher passes none
    /// (S04-03-self-sandboxing, "What keeps `wb-launcher` narrow").
    public static func checkArguments(_ arguments: [String]) throws(StartupRefusal) {
        guard arguments.count <= 1 else {
            throw StartupRefusal(.arguments, "\(arguments.count - 1) command-line arguments")
        }
    }

    /// Checks every inherited descriptor.
    public static func checkDescriptors() throws(StartupRefusal) {
        for descriptor in InheritedDescriptor.allCases {
            try check(descriptor, fd: descriptor.rawValue)
        }
    }

    /// Checks that `fd` is what `descriptor` needs.
    static func check(_ descriptor: InheritedDescriptor, fd: Int32) throws(StartupRefusal) {
        guard fcntl(fd, F_GETFD) != -1 else {
            throw refusal(descriptor, fd, "is not open")
        }
        // The profile denies fstat on a file outside the bundle, so a log
        // that is a file fails here, as S04-03-self-sandboxing, "Daemon
        // logs", wants: the log is a pipe or socket to wb-hostd.
        var info = stat()
        guard fstat(fd, &info) == 0 else {
            throw refusal(descriptor, fd, "can't be read with fstat")
        }
        let kind = info.st_mode & S_IFMT
        switch descriptor {
        case .lifeline:
            try checkPipeOrSocket(descriptor, fd: fd, kind: kind)
            // A non-blocking read would end the lifeline at once.
            guard fcntl(fd, F_GETFL) & O_NONBLOCK == 0 else {
                throw refusal(descriptor, fd, "is non-blocking")
            }
        case .log:
            try checkPipeOrSocket(descriptor, fd: fd, kind: kind)
        case .grpc:
            try checkSocket(descriptor, fd: fd, kind: kind, type: SOCK_STREAM)
            // macOS doesn't answer SO_ACCEPTCONN. A bound socket without a
            // peer is listening or about to be, and a socket that isn't
            // listening fails the first accept, which ends wb-vmd.
            guard isBoundWithoutPeer(fd) else {
                throw refusal(descriptor, fd, "is not a bound socket without a peer")
            }
        case .handoff:
            try checkSocket(descriptor, fd: fd, kind: kind, type: SOCK_DGRAM)
        }
    }

    private static func checkPipeOrSocket(
        _ descriptor: InheritedDescriptor, fd: Int32, kind: mode_t
    ) throws(StartupRefusal) {
        guard kind == S_IFIFO || kind == S_IFSOCK else {
            throw refusal(descriptor, fd, "is not a pipe or socket")
        }
    }

    private static func checkSocket(
        _ descriptor: InheritedDescriptor, fd: Int32, kind: mode_t, type: Int32
    ) throws(StartupRefusal) {
        guard kind == S_IFSOCK else {
            throw refusal(descriptor, fd, "is not a socket")
        }
        guard socketFamily(fd) == AF_UNIX else {
            throw refusal(descriptor, fd, "is not an AF_UNIX socket")
        }
        guard socketOption(fd, SOL_SOCKET, SO_TYPE) == type else {
            throw refusal(descriptor, fd, "has the wrong socket type")
        }
    }

    private static func refusal(_ descriptor: InheritedDescriptor, _ fd: Int32, _ what: String)
        -> StartupRefusal
    {
        StartupRefusal(.descriptor, "fd \(fd), the \(descriptor.name) descriptor, \(what)")
    }
}

/// An integer socket option, or nil when it can't be read.
func socketOption(_ fd: Int32, _ level: Int32, _ name: Int32) -> Int32? {
    var value: Int32 = 0
    var length = socklen_t(MemoryLayout<Int32>.size)
    guard getsockopt(fd, level, name, &value, &length) == 0 else {
        return nil
    }
    return value
}

/// Whether a Unix socket has a path of its own and no peer.
func isBoundWithoutPeer(_ fd: Int32) -> Bool {
    var address = sockaddr_un()
    var length = socklen_t(MemoryLayout<sockaddr_un>.size)
    let named = withUnsafeMutablePointer(to: &address) { pointer in
        pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &length) }
    }
    let pathOffset = MemoryLayout<sockaddr_un>.offset(of: \.sun_path) ?? 2
    guard named == 0, Int(length) > pathOffset, address.sun_path.0 != 0 else {
        return false
    }
    var peer = sockaddr_storage()
    var peerLength = socklen_t(MemoryLayout<sockaddr_storage>.size)
    let hasPeer = withUnsafeMutablePointer(to: &peer) { pointer in
        pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            getpeername(fd, $0, &peerLength)
        }
    }
    return hasPeer != 0 && errno == ENOTCONN
}

/// The address family of a socket, or nil when it can't be read.
func socketFamily(_ fd: Int32) -> Int32? {
    var address = sockaddr_storage()
    var length = socklen_t(MemoryLayout<sockaddr_storage>.size)
    let result = withUnsafeMutablePointer(to: &address) { pointer in
        pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &length) }
    }
    guard result == 0 else {
        return nil
    }
    return Int32(address.ss_family)
}
