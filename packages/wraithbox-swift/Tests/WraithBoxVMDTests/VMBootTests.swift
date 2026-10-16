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
        defer { daemon.kill() }
        let transport = try clientTransport(
            path: daemon.socketPath, version: WraithBoxVersion.version, log: RecordedLog().log)
        try await withGRPCClient(transport: transport) { grpc in
            let client = Wraithbox_Vmd_V1_VMService.Client(wrapping: grpc)
            try await bootThenForceStop(client, daemon: daemon)
            try await refuseLinkOutOfBundle(client, slot: slot)
            _ = try await client.start(startRequest(generation: 3))
            try await waitForState(client, .running)
        }
        try daemon.closeLifeline()
        #expect(try daemon.waitForExit(seconds: 30) == 0)
        #expect(!daemon.log.events("lifeline-closed").isEmpty)
        daemon.printLog()
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
    private let reaped = Mutex(false)
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
        let log = log
        let thread = Thread {
            Self.readLines(logPipe.0, into: log)
            close(handoff.0)
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

    static func readLines(_ fd: Int32, into log: RecordedLog) {
        let handle = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
        var pending = Data()
        while let chunk = try? handle.read(upToCount: 4096), !chunk.isEmpty {
            pending += chunk
            while let newline = pending.firstIndex(of: 0x0A) {
                log.append(String(decoding: pending[..<newline], as: UTF8.self))
                pending = pending[(newline + 1)...]
            }
        }
    }

    func closeLifeline() throws {
        lifeline.withLock { fd in
            close(fd)
            fd = -1
        }
    }

    /// Waits for the process to exit, and returns its exit status.
    func waitForExit(seconds: Int) throws -> Int32? {
        for _ in 0..<(seconds * 10) {
            var status: Int32 = 0
            if waitpid(pid, &status, WNOHANG) == pid {
                reaped.withLock { $0 = true }
                return (status >> 8) & 0xFF
            }
            usleep(100_000)
        }
        return nil
    }

    /// Ends the process if the test didn't see it exit.
    func kill() {
        guard !reaped.withLock({ $0 }) else {
            return
        }
        Darwin.kill(pid, SIGKILL)
        var status: Int32 = 0
        waitpid(pid, &status, 0)
    }

    /// Prints wb-vmd's log, for the pull request.
    func printLog() {
        for line in log.lines {
            print("wb-vmd: \(line)")
        }
    }
}
