// The needs-vm test of I161: the signed wb-vmd boots a macOS guest in the
// work slot, confined, and stops it. Skipped unless WB_VM_TEST_BUNDLE
// names a spike bundle. It writes the work slot's bundle under the real
// `<data>`, so it runs on the maintainer's Mac in an attended session
// (docs/agents/host-work.md): `mise run swift:test-vm`.
import Darwin
import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import Synchronization
import Testing
import WraithBoxRPC
import WraithBoxVM

@testable import WraithBoxVMD

/// The spike bundle to clone, from WB_VM_TEST_BUNDLE.
private let spikeBundle = ProcessInfo.processInfo.environment["WB_VM_TEST_BUNDLE"]

@Suite("Boot a VM (needs-vm)", .enabled(if: spikeBundle != nil), .serialized)
struct VMBootTests {
    @Test(
        "boot, watch, force stop, refuse a link out of the bundle, and stop on the lifeline",
        .timeLimit(.minutes(10)))
    func bootAndStop() async throws {
        let slot = try WorkSlot(cloning: try #require(spikeBundle))
        defer { slot.remove() }
        let daemon = try SpawnedDaemon(binary: try signedBinary())
        // Pass or fail, the output ends with how wb-vmd ended and every
        // record it logged, so a failure names its cause.
        defer { daemon.finish() }
        // The signed wb-vmd is stamped and this test process isn't, so the
        // client sends the version wb-vmd logs as started.
        let transport = try clientTransport(
            path: daemon.socketPath, version: try await daemon.startedVersion(),
            log: RecordedLog().log)
        try await withGRPCClient(transport: transport) { grpc in
            let client = Wraithbox_Vmd_V1_VMService.Client(wrapping: grpc)
            try await bootThenForceStop(client, daemon: daemon)
            try await refuseLinkOutOfBundle(client, slot: slot)
            _ = try await client.start(startRequest(generation: 3))
            try await waitForState(client, .running)
        }
        try daemon.closeLifeline()
        #expect(daemon.waitForExit(seconds: 30) == 0)
        #expect(!daemon.log.events("lifeline-closed").isEmpty)
    }

    private func bootThenForceStop(
        _ client: Wraithbox_Vmd_V1_VMService.Client<HTTP2ClientTransport.Posix>,
        daemon: SpawnedDaemon
    ) async throws {
        _ = try await client.start(startRequest(generation: 1))
        try await waitForState(client, .running)
        let devices = try #require(daemon.log.events("vm-devices").first?["devices"] as? [String])
        #expect(devices == VMDescription.fixedSet(bundlePath: "/").devices.map(\.logName))
        _ = try await client.stop(stopRequest(generation: 1, mode: .force))
        try await waitForState(client, .stopped)
    }

    private func refuseLinkOutOfBundle(
        _ client: Wraithbox_Vmd_V1_VMService.Client<HTTP2ClientTransport.Posix>, slot: WorkSlot
    ) async throws {
        try slot.linkDataDiskOutside()
        let (code, reason) = await refusal {
            _ = try await client.start(startRequest(generation: 2))
        }
        #expect(code == .failedPrecondition)
        #expect(reason == DeviceSetRule.fileOutsideBundle.rawValue)
        // Still alive and answering.
        let state = try await client.getState(Wraithbox_Vmd_V1_GetStateRequest())
        #expect(state.state.state == .stopped)
        try slot.restoreDataDisk()
    }

    private func waitForState(
        _ client: Wraithbox_Vmd_V1_VMService.Client<HTTP2ClientTransport.Posix>,
        _ wanted: Wraithbox_Vmd_V1_State
    ) async throws {
        let seen = try await client.watchState(Wraithbox_Vmd_V1_WatchStateRequest()) { response in
            for try await message in response.messages where message.state.state == wanted {
                return true
            }
            return false
        }
        #expect(seen, "never saw \(wanted)")
    }

    /// The ad-hoc signed wb-vmd that `mise run swift:sign` writes.
    private func signedBinary() throws -> String {
        if let path = ProcessInfo.processInfo.environment["WB_VMD_BINARY"] {
            return path
        }
        let package = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent()
        let path = package.appendingPathComponent(".build/signed/wb-vmd").path
        try #require(
            FileManager.default.isExecutableFile(atPath: path), "run mise run swift:sign first")
        return path
    }
}

/// The work slot's bundle, an APFS clone of a spike bundle with its
/// guest-written files renamed (S06-05-disks, "Bundle files").
final class WorkSlot {
    let path: String
    private let outside: String
    private var createdDirectories: [String] = []

    init(cloning spike: String) throws {
        let data = HostPaths.data(home: SlotResolution.homeDirectory())
        path = data + "/" + HostPaths.bundle(of: .work)
        outside = data + "/vms/vmd-test-outside.wbdisk"
        // Never touch a slot that is already there.
        try #require(!FileManager.default.fileExists(atPath: path), "\(path) already exists")
        // From here on the slot is this test's: a failure removes what it
        // made, so no half-made clone stays in the real <data>.
        do {
            try makeSlot(cloning: spike, data: data)
        } catch {
            remove()
            throw error
        }
    }

    private func makeSlot(cloning spike: String, data: String) throws {
        for directory in [data, data + "/vms"]
        where !FileManager.default.fileExists(atPath: directory) {
            try FileManager.default.createDirectory(
                atPath: directory, withIntermediateDirectories: false)
            createdDirectories.append(directory)
        }
        try #require(clonefile(spike, path, UInt32(CLONE_NOFOLLOW)) == 0, "clonefile: \(errno)")
        try renameSpikeFiles()
        try addMissingDisk(.dataDisk, bytes: 8 << 30)
        try addMissingDisk(.toolsDisk, bytes: 64 << 20)
    }

    func linkDataDiskOutside() throws {
        let disk = path + "/" + BundleFile.dataDisk.rawValue
        try FileManager.default.moveItem(atPath: disk, toPath: outside)
        try FileManager.default.createSymbolicLink(atPath: disk, withDestinationPath: outside)
    }

    func restoreDataDisk() throws {
        let disk = path + "/" + BundleFile.dataDisk.rawValue
        try FileManager.default.removeItem(atPath: disk)
        try FileManager.default.moveItem(atPath: outside, toPath: disk)
    }

    /// Deletes the slot, and the directories this test made for it.
    func remove() {
        try? FileManager.default.removeItem(atPath: outside)
        try? FileManager.default.removeItem(atPath: path)
        for directory in createdDirectories.reversed() {
            rmdir(directory)
        }
    }

    private func renameSpikeFiles() throws {
        let names = [
            ("aux.img", BundleFile.auxiliaryStorage), ("sys.img", .systemDisk),
            ("data.img", .dataDisk),
        ]
        for (old, new) in names where FileManager.default.fileExists(atPath: path + "/" + old) {
            try #require(
                renamex_np(path + "/" + old, path + "/" + new.rawValue, UInt32(RENAME_EXCL)) == 0)
        }
    }

    private func addMissingDisk(_ file: BundleFile, bytes: off_t) throws {
        let disk = path + "/" + file.rawValue
        guard !FileManager.default.fileExists(atPath: disk) else {
            return
        }
        let fd = open(disk, O_RDWR | O_CREAT | O_EXCL, 0o600)
        try #require(fd >= 0)
        defer { close(fd) }
        try #require(ftruncate(fd, bytes) == 0)
    }
}

/// wb-vmd started with the descriptors of its start interface
/// (S04-03-self-sandboxing, "How `wb-vmd` is started").
final class SpawnedDaemon: Sendable {
    let pid: pid_t
    let socketPath: String
    let log = RecordedLog()
    private let lifeline = Mutex<Int32>(-1)
    /// The wait status, once the process has been reaped.
    private let waitStatus = Mutex<Int32?>(nil)
    private let readerDone = Mutex(false)
    private let directory: TemporaryDirectory

    init(binary: String) throws {
        directory = try TemporaryDirectory()
        socketPath = directory.path + "/vmd.sock"
        let listening = try listeningSocket(at: socketPath)
        let lifelinePipe = try makePipe()
        let logPipe = try makePipe()
        let handoff = try makeSocketPair(SOCK_DGRAM)
        pid = try Self.spawn(
            binary, descriptors: [lifelinePipe.0, logPipe.1, listening, handoff.1])
        closeAll([lifelinePipe.0, logPipe.1, listening, handoff.1])
        lifeline.withLock { $0 = lifelinePipe.1 }
        let thread = Thread { [self] in
            Self.readLines(logPipe.0, into: log)
            close(handoff.0)
            readerDone.withLock { $0 = true }
        }
        thread.start()
    }

    /// Spawns `binary` with no arguments, WB_VMD_SLOT=work, and the given
    /// descriptors as 0, 2, 3 and 4. Nothing else is inherited.
    static func spawn(
        _ binary: String, descriptors: [Int32], environment: [String] = ["WB_VMD_SLOT=work"]
    ) throws -> pid_t {
        var actions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&actions)
        defer { posix_spawn_file_actions_destroy(&actions) }
        for (fd, target) in zip(descriptors, InheritedDescriptor.allCases.map(\.rawValue)) {
            posix_spawn_file_actions_adddup2(&actions, fd, target)
        }
        var attributes: posix_spawnattr_t?
        posix_spawnattr_init(&attributes)
        defer { posix_spawnattr_destroy(&attributes) }
        posix_spawnattr_setflags(&attributes, Int16(POSIX_SPAWN_CLOEXEC_DEFAULT))
        let arguments: [UnsafeMutablePointer<CChar>?] = [strdup(binary), nil]
        let variables: [UnsafeMutablePointer<CChar>?] = environment.map { strdup($0) } + [nil]
        defer {
            for string in arguments + variables {
                free(string)
            }
        }
        var pid: pid_t = 0
        let result = posix_spawn(&pid, binary, &actions, &attributes, arguments, variables)
        try #require(result == 0, "posix_spawn: \(result)")
        return pid
    }

    /// Reads `fd` until end of file, adds each line to `log` as soon as it
    /// arrives, and closes `fd`. read(2) returns what the pipe holds.
    /// FileHandle's `read(upToCount:)` waits for the whole count or end of
    /// file, which keeps a running wb-vmd's few records from the test.
    static func readLines(_ fd: Int32, into log: RecordedLog) {
        defer { close(fd) }
        var pending: [UInt8] = []
        var buffer = [UInt8](repeating: 0, count: 4096)
        while true {
            let count = buffer.withUnsafeMutableBytes { read(fd, $0.baseAddress, $0.count) }
            if count < 0 && errno == EINTR {
                continue
            }
            guard count > 0 else {
                return
            }
            pending += buffer.prefix(count)
            while let newline = pending.firstIndex(of: 0x0A) {
                log.append(String(decoding: pending[..<newline], as: UTF8.self))
                pending.removeFirst(newline + 1)
            }
        }
    }

    /// The build version from wb-vmd's `started` record. It waits up to
    /// 10 seconds for the record, and requires a stamped build.
    func startedVersion() async throws -> String {
        for _ in 0..<100 where log.events("started").isEmpty {
            try await Task.sleep(for: .milliseconds(100))
        }
        let records = log.lines.suffix(5).joined(separator: "\n")
        let started = try #require(
            log.events("started").first,
            "wb-vmd logged no started record, and it \(endDescription()). Its last records:\n\(records)"
        )
        let version = try #require(started["version"] as? String)
        try #require(!version.isEmpty, "wb-vmd is unstamped: run mise run swift:sign")
        return version
    }

    func closeLifeline() throws {
        lifeline.withLock { fd in
            close(fd)
            fd = -1
        }
    }

    /// Waits for the process to exit, and returns its exit status, or nil
    /// when it is still running or ended on a signal.
    func waitForExit(seconds: Int) -> Int32? {
        for _ in 0..<(seconds * 10) {
            if let status = reap() {
                return status & 0x7F == 0 ? (status >> 8) & 0xFF : nil
            }
            usleep(100_000)
        }
        return nil
    }

    /// The wait status once the process has exited, without waiting for it.
    private func reap() -> Int32? {
        waitStatus.withLock { stored in
            var status: Int32 = 0
            if stored == nil, waitpid(pid, &status, WNOHANG) == pid {
                stored = status
            }
            return stored
        }
    }

    /// How the process ended, or that it hasn't, as the end of a sentence
    /// that starts with "wb-vmd".
    func endDescription() -> String {
        guard let status = reap() else {
            return "is still running"
        }
        let signal = status & 0x7F
        return signal == 0
            ? "exited with status \((status >> 8) & 0xFF)" : "ended on signal \(signal)"
    }

    /// Ends the process if it is still running, and prints how it ended
    /// and every record it logged, for the pull request and for a failure.
    func finish() {
        let ended = endDescription()
        if reap() == nil {
            Darwin.kill(pid, SIGKILL)
            var status: Int32 = 0
            waitpid(pid, &status, 0)
            waitStatus.withLock { $0 = status }
        }
        // The reader ends at end of file, once the process is gone.
        for _ in 0..<20 where !readerDone.withLock({ $0 }) {
            usleep(100_000)
        }
        print("wb-vmd \(ended) at the end of the test. Its records:")
        for line in log.lines {
            print("wb-vmd: \(line)")
        }
    }
}
