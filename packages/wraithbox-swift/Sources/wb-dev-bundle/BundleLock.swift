// Which bundle has a VM running: the process that runs one holds an
// exclusive flock(2) on the bundle's lock file for as long as it runs
// (I350). So a bundle is never cloned, provisioned or removed while its VM
// runs, and `stop` finds the process to signal.
import Foundation

/// The lock on a bundle, held while this process uses it.
final class BundleLock {
    static let fileName = "dev-bundle.lock"

    private let fd: Int32

    private init(fd: Int32) {
        self.fd = fd
    }

    deinit {
        // Closing the last descriptor releases the lock.
        close(fd)
    }

    /// Takes the lock of the bundle at `bundle`, and writes this process's
    /// ID into the lock file. Refuses when another process holds it. The
    /// descriptor is close-on-exec, so no child process keeps the lock.
    static func take(bundle: String) throws -> BundleLock {
        let path = bundle + "/" + fileName
        let fd = open(path, O_RDWR | O_CREAT | O_NOFOLLOW | O_CLOEXEC, 0o644)
        guard fd >= 0 else {
            throw Failure.posix("open", path)
        }
        // `holder` probes with a shared lock for a moment, so a failed try
        // is tried once more before it counts as a running VM.
        guard flock(fd, LOCK_EX | LOCK_NB) == 0 || retryAfterProbe(fd) else {
            let holder = readHolder(fd)
            close(fd)
            throw Refusal(.vmRunning, bundle, "process \(holder) runs its VM")
        }
        let text = Array("\(getpid())\n".utf8)
        guard ftruncate(fd, 0) == 0, pwrite(fd, text, text.count, 0) == text.count else {
            let failure = Failure.posix("write", path)
            close(fd)
            throw failure
        }
        return BundleLock(fd: fd)
    }

    /// The process that holds the lock of the bundle at `bundle`, or nil
    /// when none does. Creates no lock file.
    static func holder(bundle: String) -> pid_t? {
        let fd = open(bundle + "/" + fileName, O_RDONLY | O_NOFOLLOW | O_CLOEXEC)
        guard fd >= 0 else {
            return nil
        }
        defer { close(fd) }
        if flock(fd, LOCK_SH | LOCK_NB) == 0 {
            flock(fd, LOCK_UN)
            return nil
        }
        return readHolder(fd)
    }

    private static func retryAfterProbe(_ fd: Int32) -> Bool {
        usleep(100_000)
        return flock(fd, LOCK_EX | LOCK_NB) == 0
    }

    /// The process ID in a lock file, or 0 when it holds none.
    private static func readHolder(_ fd: Int32) -> pid_t {
        var buffer = [UInt8](repeating: 0, count: 16)
        let count = pread(fd, &buffer, buffer.count, 0)
        guard count > 0 else {
            return 0
        }
        let text = String(decoding: buffer.prefix(count), as: UTF8.self)
        return pid_t(text.trimmingCharacters(in: .whitespacesAndNewlines)) ?? 0
    }
}
