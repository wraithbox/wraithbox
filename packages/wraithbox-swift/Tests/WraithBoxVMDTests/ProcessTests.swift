// C11-host-daemons, "Slot": the built wb-vmd, started with WB_VMD_SLOT
// unset, unknown, or the build slot, logs the rule before it confines
// itself and exits 1. None of these cases reads host state, so they run
// anywhere the package builds.
import Darwin
import Foundation
import Synchronization
import Testing

@testable import WraithBoxVMD

@Suite("wb-vmd process")
struct ProcessTests {
    @Test(
        "a slot it doesn't serve is logged with its rule before confinement, and wb-vmd exits 1",
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
