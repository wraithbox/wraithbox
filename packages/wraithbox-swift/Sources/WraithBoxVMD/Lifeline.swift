// The lifeline (S04-03-self-sandboxing, "How `wb-vmd` is started";
// S04-07-supervision, "When `wb-hostd` stops"). wb-vmd reads fd 0 until
// end of file. End of file, or a read error, means its spawner is gone:
// wb-vmd stops its VM at once and exits, so no VM outlives the process
// that asked for it (S06-11-reconnect-and-recovery, "Lost").
import Darwin
import Foundation

/// Watches the lifeline descriptor on a thread of its own.
public enum Lifeline {
    /// Starts a thread that reads `fd` until end of file or an error,
    /// discarding what it reads, then calls `onClose` once with the
    /// reason.
    public static func watch(fd: Int32, onClose: @escaping @Sendable (String) -> Void) {
        let thread = Thread {
            onClose(readUntilClosed(fd))
        }
        thread.name = "wb-vmd lifeline"
        thread.start()
    }

    /// Reads until end of file or an error other than EINTR, and says
    /// which.
    static func readUntilClosed(_ fd: Int32) -> String {
        var buffer = [UInt8](repeating: 0, count: 64)
        while true {
            let count = buffer.withUnsafeMutableBytes { read(fd, $0.baseAddress, $0.count) }
            if count > 0 {
                continue
            }
            if count == 0 {
                return "end of file"
            }
            if errno == EINTR {
                continue
            }
            return "read error: " + String(cString: strerror(errno))
        }
    }
}
