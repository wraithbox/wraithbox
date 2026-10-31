// C11-host-daemons, "Slot": the built wb-vmd, started with WB_VMD_SLOT
// unset, unknown, or the build slot, logs the rule before it confines
// itself and exits 1. None of these cases reads host state but the macOS
// version. wb-vmd refuses a macOS older than 27 first, so on such a host
// only that refusal is tested (HostRequirement).
import Darwin
import Foundation
import Synchronization
import Testing
import WraithBoxVM

@testable import WraithBoxVMD

private let hostSupported = HostRequirement.supports(
    ProcessInfo.processInfo.operatingSystemVersion)

@Suite("wb-vmd process")
struct ProcessTests {
    @Test(
        "an older macOS is logged with vmd-host-unsupported before confinement, and wb-vmd exits 1",
        .enabled(if: !hostSupported, "the host runs macOS 27 or later"))
    func refusedHost() throws {
        let (status, log) = try runBuiltDaemon(environment: ["WB_VMD_SLOT=work"])
        #expect(status == 1)
        #expect(log.rules == [StartupRule.hostUnsupported.rawValue])
        #expect(log.events("confined").isEmpty)
    }

    @Test(
        "a slot it doesn't serve is logged with its rule before confinement, and wb-vmd exits 1",
        .enabled(if: hostSupported, "wb-vmd refuses a macOS older than 27 first"),
        arguments: [
            ([String](), StartupRule.slotMissing),
            (["WB_VMD_SLOT=nope"], .slotUnknown),
            (["WB_VMD_SLOT=build"], .slotBuildUnsupported),
        ])
    func refusedSlot(environment: [String], rule: StartupRule) throws {
        let (status, log) = try runBuiltDaemon(environment: environment)
        #expect(status == 1)
        #expect(log.rules == [rule.rawValue])
        #expect(log.events("start-refused").count == 1)
        #expect(log.events("confined").isEmpty)
    }

    // The needs-vm test waits for wb-vmd's started record while wb-vmd
    // runs, so its log reader must hand over each line as it arrives, not
    // at end of file.
    @Test("the log reader adds a record while its writer is still open")
    func logReaderDoesNotWaitForEndOfFile() async throws {
        let (readEnd, writeEnd) = try makePipe()
        let log = RecordedLog()
        let done = Mutex(false)
        let thread = Thread {
            SpawnedDaemon.readLines(readEnd, into: log)
            done.withLock { $0 = true }
        }
        thread.start()
        writeAll(writeEnd, "{\"event\":\"started\"}\n{\"event\":")
        for _ in 0..<50 where log.lines.isEmpty {
            try await Task.sleep(for: .milliseconds(100))
        }
        #expect(log.lines == ["{\"event\":\"started\"}"])
        close(writeEnd)
        for _ in 0..<50 where !done.withLock({ $0 }) {
            try await Task.sleep(for: .milliseconds(100))
        }
        #expect(done.withLock { $0 })
        #expect(log.lines.count == 1)
    }

    /// Runs the wb-vmd next to the test bundle with a lifeline and a log
    /// pipe, and returns its exit status and log.
    private func runBuiltDaemon(environment: [String]) throws -> (Int32, RecordedLog) {
        let binary = try builtBinary()
        let lifeline = try makePipe()
        let logPipe = try makePipe()
        let pid = try SpawnedDaemon.spawn(
            binary, descriptors: [lifeline.0, logPipe.1], environment: environment)
        closeAll([lifeline.0, logPipe.1])
        defer { close(lifeline.1) }
        let log = RecordedLog()
        SpawnedDaemon.readLines(logPipe.0, into: log)
        var status: Int32 = 0
        waitpid(pid, &status, 0)
        return ((status >> 8) & 0xFF, log)
    }

    /// The wb-vmd executable the same build made, next to the test bundle.
    private func builtBinary() throws -> String {
        // This image is in Contents/MacOS of the test bundle, which is in
        // the products directory next to wb-vmd.
        var info = Dl_info()
        try #require(dladdr(#dsohandle, &info) != 0 && info.dli_fname != nil)
        var products = URL(fileURLWithPath: String(cString: info.dli_fname))
        for _ in 0..<4 {
            products.deleteLastPathComponent()
        }
        let path = products.appendingPathComponent("wb-vmd").path
        try #require(FileManager.default.isExecutableFile(atPath: path), "no wb-vmd at \(path)")
        return path
    }
}
