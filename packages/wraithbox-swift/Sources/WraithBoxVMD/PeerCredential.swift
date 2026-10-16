// The peer check on wb-vmd's gRPC socket (S04-architecture,
// "Inter-process contracts": the peer's identity is checked on every
// connection; SEC12-least-privilege). The run directory's mode is the
// first layer. This is the second: a connection whose peer user ID isn't
// wb-vmd's own is closed before a byte is read from it, and logged with
// the rule `ipc-peer-uid`.
import Darwin

/// Who is at the other end of a Unix socket, as the kernel says.
public struct PeerCredential: Sendable, Equatable {
    public let uid: uid_t
    public let pid: pid_t

    public init(uid: uid_t, pid: pid_t) {
        self.uid = uid
        self.pid = pid
    }

    /// Reads the peer of a connected Unix socket with LOCAL_PEERCRED and
    /// LOCAL_PEERPID. Returns nil when either can't be read, or the
    /// credential's version isn't the one this code knows.
    public static func read(fd: Int32) -> PeerCredential? {
        var credential = xucred()
        var length = socklen_t(MemoryLayout<xucred>.size)
        guard getsockopt(fd, SOL_LOCAL, LOCAL_PEERCRED, &credential, &length) == 0 else {
            return nil
        }
        guard let pid = socketOption(fd, SOL_LOCAL, LOCAL_PEERPID) else {
            return nil
        }
        return from(credential, pid: pid)
    }

    /// A credential from the kernel's record, or nil when its version
    /// isn't XUCRED_VERSION.
    static func from(_ credential: xucred, pid: pid_t) -> PeerCredential? {
        guard credential.cr_version == XUCRED_VERSION else {
            return nil
        }
        return PeerCredential(uid: credential.cr_uid, pid: pid)
    }
}

/// The rule a refused connection names.
public enum ConnectionRule: String, Sendable, Equatable, CaseIterable {
    /// The peer's user ID isn't wb-vmd's, or can't be read.
    case peerUID = "ipc-peer-uid"
    /// No version record before the deadline.
    case versionTimeout = "peer-version-timeout"
    /// A record with the wrong magic, a length over the bound, or a byte
    /// that isn't printable ASCII.
    case versionMalformed = "peer-version-malformed"
    /// The peer's version or wb-vmd's own is empty: an unstamped build.
    case versionUnstamped = "peer-version-unstamped"
    /// The versions differ.
    case versionMismatch = "peer-version-mismatch"
}

/// Why a connection was refused.
public struct ConnectionRefusal: Error, Sendable, Equatable {
    public let rule: ConnectionRule
    public let detail: String

    public init(_ rule: ConnectionRule, _ detail: String) {
        self.rule = rule
        self.detail = detail
    }
}

/// Decides whether a peer may talk to wb-vmd.
public struct PeerCheck: Sendable {
    public let ownUID: uid_t

    public init(ownUID: uid_t = getuid()) {
        self.ownUID = ownUID
    }

    /// Accepts a peer of wb-vmd's own user ID. A peer whose credential
    /// couldn't be read is refused too.
    public func check(_ peer: PeerCredential?) throws(ConnectionRefusal) -> PeerCredential {
        guard let peer else {
            throw ConnectionRefusal(.peerUID, "the peer's credential can't be read")
        }
        guard peer.uid == ownUID else {
            throw ConnectionRefusal(.peerUID, "peer user ID \(peer.uid) is not \(ownUID)")
        }
        return peer
    }
}
