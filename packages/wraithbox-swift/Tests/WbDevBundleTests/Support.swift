// A throwaway spike cache in a temporary directory, and a log that keeps
// its lines. No test reads or writes the real spike cache or boots a VM.
import Foundation
import Synchronization

@testable import wb_dev_bundle

/// A cache directory with `restore-images/` in a new temporary directory,
/// removed with it.
final class TestCache {
    let base: String
    let cache: SpikeCache
    let lines = Lines()

    var root: String { cache.root }
    var log: Log {
        let lines = self.lines
        return Log { lines.append($0) }
    }

    init() throws {
        var template = Array("/tmp/wb-dev-bundle-XXXXXX".utf8CString)
        guard let made = mkdtemp(&template) else {
            throw POSIXError(.EIO)
        }
        // /tmp is a symbolic link to /private/tmp; the cache path has none.
        base = URL(fileURLWithPath: String(cString: made)).resolvingSymlinksInPath().path
        cache = SpikeCache(root: base + "/wraithbox-spikes")
        try makeDirectory(SpikeCache.restoreImages)
    }

    deinit {
        try? FileManager.default.removeItem(atPath: base)
    }

    /// The absolute path of `relative` in the cache.
    func path(_ relative: String) -> String {
        root + "/" + relative
    }

    func makeDirectory(_ relative: String) throws {
        try FileManager.default.createDirectory(
            atPath: path(relative), withIntermediateDirectories: true)
    }

    func write(_ relative: String, _ text: String = "x") throws {
        let file = path(relative)
        try FileManager.default.createDirectory(
            atPath: (file as NSString).deletingLastPathComponent,
            withIntermediateDirectories: true)
        try Data(text.utf8).write(to: URL(fileURLWithPath: file))
    }

    /// Replaces the process ID in a bundle's lock file in place, so a lock
    /// this process holds names another process.
    func setLockHolder(_ relative: String, _ pid: pid_t) throws {
        let handle = try FileHandle(
            forWritingTo: URL(fileURLWithPath: path(relative + "/" + BundleLock.fileName)))
        defer { try? handle.close() }
        try handle.truncate(atOffset: 0)
        try handle.write(contentsOf: Data("\(pid)\n".utf8))
    }

    func link(_ relative: String, to destination: String) throws {
        try FileManager.default.createSymbolicLink(
            atPath: path(relative), withDestinationPath: destination)
    }

    func exists(_ relative: String) -> Bool {
        var info = stat()
        return lstat(path(relative), &info) == 0
    }

    /// A bundle as `install` leaves it: some bundle files and its record.
    func installedBundle(_ relative: String, booted: Bool = false) throws {
        try write(relative + "/hw.bin")
        try write(relative + "/sys.wbdisk")
        let record = BundleRecord(
            restoreImage: path("restore-images/test.ipsw"), buildVersion: "26A434", cpuCount: 4,
            memoryGiB: 4, installedAt: Date(timeIntervalSince1970: 0),
            bootedAt: booted ? Date(timeIntervalSince1970: 60) : nil)
        try record.write(bundle: path(relative))
    }
}

final class Lines: Sendable {
    private let stored = Mutex<[String]>([])

    func append(_ line: String) {
        stored.withLock { $0.append(line) }
    }

    var all: [String] { stored.withLock { $0 } }

    /// Whether a logged line names `rule` as the rule of a refusal.
    func refused(by rule: Rule) -> Bool {
        all.contains { $0.contains("refused") && $0.hasSuffix("(rule: \(rule.rawValue))") }
    }
}
