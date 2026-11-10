// The build password's input: the read end of the build password pipe,
// descriptor 5 in the build slot only (S04-03-self-sandboxing, "How
// `wb-vmd` is started"; S15-04-guest-admin, "Build"). wb-vmd reads it once,
// for a start with provisioning options, and closes it. The bytes come
// from wb-guestadmin-build through wb-launcher, but the reader bounds and
// checks them as it would any input. The password never reaches a log, a
// refusal or a file (SEC04-no-guest-secrets). Nothing reads it yet: the
// start path refuses provisioning in every slot until ProvisioningCheck
// is in it (#629).
import Darwin

/// Reads the build password from its pipe, once.
@MainActor
public final class ProvisioningInput {
    /// The most bytes a password may have.
    public nonisolated static let maxBytes = 256
    /// How long the reader waits for the end of the password.
    public nonisolated static let deadline = Duration.seconds(5)

    private var fd: Int32?

    /// Takes `fd`, the pipe's read end, and closes it after the read.
    public init(fd: Int32) {
        self.fd = fd
    }

    /// Reads the password to end of file within `deadline`, and closes the
    /// pipe whatever the outcome. Throws
    /// `vmd-provisioning-password-invalid` for an empty, oversized,
    /// non-ASCII or late password, and for a second read. No refusal holds
    /// a byte of the password.
    public func read(deadline: Duration = deadline) throws(VMRefusal) -> String {
        guard let fd else {
            throw Self.refusal("the password was read already")
        }
        self.fd = nil
        defer { close(fd) }
        var bytes = try Self.readToEnd(fd, deadline: deadline)
        defer { Self.wipe(&bytes) }
        guard !bytes.isEmpty else {
            throw Self.refusal("the password is empty")
        }
        // Printable ASCII: the provisioning options take a string, and a
        // control byte or a multi-byte character has no place in it.
        guard bytes.allSatisfy({ (0x20...0x7e).contains($0) }) else {
            throw Self.refusal("the password is not printable ASCII")
        }
        return String(decoding: bytes, as: UTF8.self)
    }

    /// The bytes up to end of file, at most maxBytes, before the deadline.
    /// It reads into one buffer of maxBytes + 1 that never grows, so no
    /// reallocation leaves a copy behind, and wipes it on every exit. The
    /// one copy it returns is the caller's to wipe.
    static func readToEnd(_ fd: Int32, deadline: Duration) throws(VMRefusal) -> [UInt8] {
        let clock = ContinuousClock()
        let end = clock.now + deadline
        var buffer = [UInt8](repeating: 0, count: maxBytes + 1)
        defer { wipe(&buffer) }
        var filled = 0
        while true {
            // A poll that a signal interrupted waits again, so the read
            // never blocks past the deadline.
            guard try waitReadable(fd, remaining: end - clock.now) else {
                continue
            }
            let count = buffer.withUnsafeMutableBytes {
                Darwin.read(fd, $0.baseAddress! + filled, $0.count - filled)
            }
            if count == 0 {
                return Array(buffer[..<filled])
            }
            guard count > 0 else {
                if errno == EINTR || errno == EAGAIN {
                    continue
                }
                throw refusal("read failed: \(String(cString: strerror(errno)))")
            }
            filled += count
            guard filled <= maxBytes else {
                throw refusal("the password is over \(maxBytes) bytes")
            }
        }
    }

    /// Waits until `fd` can be read or is at end of file, and returns
    /// true, or false when a signal interrupted the wait. Refuses once
    /// `remaining` is gone.
    private static func waitReadable(_ fd: Int32, remaining: Duration) throws(VMRefusal) -> Bool {
        let milliseconds =
            remaining.components.seconds * 1000
            + remaining.components.attoseconds / 1_000_000_000_000_000
        guard milliseconds > 0 else {
            throw refusal("the password did not end within the deadline")
        }
        var entry = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
        let ready = poll(&entry, 1, Int32(clamping: milliseconds))
        guard ready != 0 else {
            throw refusal("the password did not end within the deadline")
        }
        guard ready > 0 || errno == EINTR else {
            throw refusal("poll failed: \(String(cString: strerror(errno)))")
        }
        return ready > 0
    }

    private static func wipe(_ bytes: inout [UInt8]) {
        bytes.withUnsafeMutableBytes { raw in
            _ = memset_s(raw.baseAddress, raw.count, 0, raw.count)
        }
    }

    private static func refusal(_ detail: String) -> VMRefusal {
        VMRefusal(.provisioningPasswordInvalid, .failedPrecondition, detail)
    }
}
