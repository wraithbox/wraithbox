// The version record that opens every host connection
// (S04-architecture, "The version record"): the four bytes
// `WBV1`, one length byte, then that many bytes of the sender's build
// version. A length of 0 is an unstamped build, which has no version
// (S10-02-packaging-and-signing, "One build version"). Each side sends
// its record first, then reads the peer's, and refuses a peer of another
// version. The parser reads at most `maxLength` bytes.

import WraithBoxVM

/// The version record's form and checks.
public enum VersionRecord {
    public static let magic: [UInt8] = Array("WBV1".utf8)
    public static let maxVersionBytes = 64
    /// The most bytes a record can take.
    public static let maxLength = magic.count + 1 + maxVersionBytes

    /// What `parse` found at the start of the bytes.
    public enum Parsed: Sendable, Equatable {
        /// Not enough bytes yet for a whole record.
        case needMore
        /// A record with a version, taking `length` bytes.
        case version(String, length: Int)
        /// A record of length 0: an unstamped peer.
        case unstamped(length: Int)
        /// Not a version record.
        case malformed(String)
    }

    /// The record for `version`. An empty version, or one that isn't 1 to
    /// 64 bytes of printable ASCII, is sent as an unstamped record: it
    /// isn't a version any peer accepts.
    public static func encode(_ version: String) -> [UInt8] {
        let bytes = Array(version.utf8)
        guard bytes.count <= maxVersionBytes, bytes.allSatisfy(isPrintable) else {
            return magic + [0]
        }
        return magic + [UInt8(bytes.count)] + bytes
    }

    /// Parses the record at the start of `bytes`. It looks at no more
    /// than `maxLength` bytes.
    public static func parse(_ bytes: [UInt8]) -> Parsed {
        let head = bytes.prefix(magic.count)
        guard head.elementsEqual(magic.prefix(head.count)) else {
            return .malformed("wrong magic")
        }
        guard bytes.count > magic.count else {
            return .needMore
        }
        let length = Int(bytes[magic.count])
        let start = magic.count + 1
        if length == 0 {
            return .unstamped(length: start)
        }
        guard length <= maxVersionBytes else {
            return .malformed("length \(length) is over \(maxVersionBytes)")
        }
        guard bytes.count >= start + length else {
            return .needMore
        }
        let version = bytes[start..<(start + length)]
        guard version.allSatisfy(isPrintable) else {
            return .malformed("a version byte is not printable ASCII")
        }
        return .version(String(decoding: version, as: UTF8.self), length: start + length)
    }

    /// Compares the peer's version with wb-vmd's own, in the order and
    /// with the rules of Go's `version.Check`. They compare as exact
    /// bytes: host processes have no compatibility rules.
    public static func check(peer: String?, own: String) throws(ConnectionRefusal) {
        guard let peer, WraithBoxVersion.isVersionForm(Array(own.utf8)) else {
            throw ConnectionRefusal(
                .versionUnstamped,
                "peer \(peer ?? "unstamped"), own \(own.isEmpty ? "unstamped" : own)")
        }
        guard WraithBoxVersion.isVersionForm(Array(peer.utf8)) else {
            throw ConnectionRefusal(.versionForm, "peer \(peer), own \(own)")
        }
        guard peer == own else {
            throw ConnectionRefusal(.versionMismatch, "peer \(peer), own \(own)")
        }
    }

    static func isPrintable(_ byte: UInt8) -> Bool {
        byte >= 0x20 && byte <= 0x7E
    }
}
