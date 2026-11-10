// The needs-vm test of I654: the signed wb-vmd, in the build slot and
// confined with the install rules, installs macOS from a restore image
// into a new build bundle, then boots the bundle and stops it. Skipped
// unless WB_VM_TEST_RESTORE_IMAGE names a restore image. It writes the
// build bundle and a clone of the restore image under the real `<data>`,
// and the install calls Apple's signing server over the host's network
// (X30-installer-trust), so it runs on the maintainer's Mac in an
// attended session (docs/agents/host-work.md):
// `mise run swift:test-vm`.
import Darwin
import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import Testing
import WraithBoxRPC
import WraithBoxVM

@testable import WraithBoxVMD

/// The restore image to clone, from WB_VM_TEST_RESTORE_IMAGE.
private let restoreImage = ProcessInfo.processInfo.environment["WB_VM_TEST_RESTORE_IMAGE"]

@Suite("Install macOS (needs-vm)", .enabled(if: restoreImage != nil), .serialized)
struct VMInstallTests {
    @Test(
        "install from the restore image, boot the build bundle, and stop it",
        .timeLimit(.minutes(60)))
    func installAndBoot() async throws {
        let slot = try BuildSlot(cloning: try #require(restoreImage))
        defer { slot.remove() }
        // Nothing reads the build password yet (#629), but the build slot
        // refuses to start without its pipe.
        let password = try makePipe()
        defer { close(password.1) }
        let daemon = try SpawnedDaemon(
            binary: try signedBinary(), slot: .build, provisioning: password.0)
        close(password.0)
        defer { daemon.finish() }
        let transport = try clientTransport(
            path: daemon.socketPath, version: try await daemon.startedVersion(),
            log: RecordedLog().log)
        try await withGRPCClient(transport: transport) { grpc in
            let client = Wraithbox_Vmd_V1_VMService.Client(wrapping: grpc)
            try await install(client)
            #expect(slot.contents() == BundleFile.allCases.map(\.rawValue).sorted())
            try await bootAndStop(client, daemon: daemon)
        }
        try daemon.closeLifeline()
        #expect(daemon.waitForExit(seconds: 30) == 0)
    }

    /// Installs, and expects rising percents that end at 100 and no error.
    private func install(
        _ client: Wraithbox_Vmd_V1_VMService.Client<HTTP2ClientTransport.Posix>
    ) async throws {
        var request = Wraithbox_Vmd_V1_InstallRequest()
        request.cpuCount = 4
        request.memoryMib = 4096
        let clock = ContinuousClock()
        let began = clock.now
        let messages = try await client.install(request) { response in
            var all: [Wraithbox_Vmd_V1_InstallResponse] = []
            for try await message in response.messages {
                all.append(message)
            }
            return all
        }
        print("measure: the install took \(clock.now - began)")
        let percents = messages.map(\.percent)
        #expect(!messages.contains { $0.hasError }, "\(messages.filter(\.hasError))")
        #expect(percents.last == 100)
        #expect(percents == percents.sorted())
    }

    /// Boots the installed bundle with the build slot's fixed set, which
    /// has no network device, and force stops it.
    private func bootAndStop(
        _ client: Wraithbox_Vmd_V1_VMService.Client<HTTP2ClientTransport.Posix>,
        daemon: SpawnedDaemon
    ) async throws {
        _ = try await client.start(startRequest(generation: 1))
        try await waitForState(client, .running)
        let devices = try #require(daemon.log.events("vm-devices").last?["devices"] as? [String])
        #expect(
            devices
                == VMDescription.fixedSet(bundlePath: "/", slot: .build).devices.map(\.logName))
        _ = try await client.stop(stopRequest(generation: 1, mode: .force))
        try await waitForState(client, .stopped)
    }
}

/// The build slot: a new build bundle with the two files wb-hostd puts in
/// it, and an APFS clone of a restore image at the fixed path
/// (S04-10-vmd-install, "The build bundle", "The restore image").
final class BuildSlot {
    let path: String
    private let restoreImagePath: String
    private var made: [String] = []
    private var createdDirectories: [String] = []

    init(cloning image: String) throws {
        let data = HostPaths.data(home: SlotResolution.homeDirectory())
        path = data + "/" + HostPaths.bundle(of: .build)
        restoreImagePath = data + "/" + HostPaths.restoreImage
        // Never touch a bundle or an image that is already there.
        for existing in [path, restoreImagePath] {
            try #require(
                !FileManager.default.fileExists(atPath: existing), "\(existing) already exists")
        }
        // From here on the paths are this test's: a failure removes what
        // it made, so nothing half-made stays in the real <data>.
        do {
            try makeSlot(cloning: image, data: data)
        } catch {
            remove()
            throw error
        }
    }

    private func makeSlot(cloning image: String, data: String) throws {
        for directory in [data, data + "/images"]
        where !FileManager.default.fileExists(atPath: directory) {
            try FileManager.default.createDirectory(
                atPath: directory, withIntermediateDirectories: false)
            createdDirectories.append(directory)
        }
        try #require(
            clonefile(image, restoreImagePath, UInt32(CLONE_NOFOLLOW)) == 0, "clonefile: \(errno)")
        made.append(restoreImagePath)
        try #require(mkdir(path, 0o700) == 0, "mkdir: \(errno)")
        made.append(path)
        try addDisk(.dataDisk, bytes: 8 << 30)
        try addDisk(.toolsDisk, bytes: 64 << 20)
    }

    /// The names in the bundle, sorted.
    func contents() -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: path)) ?? []).sorted()
    }

    /// Deletes the bundle, the clone, and the directories this test made.
    func remove() {
        for item in made.reversed() {
            try? FileManager.default.removeItem(atPath: item)
        }
        for directory in createdDirectories.reversed() {
            rmdir(directory)
        }
    }

    private func addDisk(_ file: BundleFile, bytes: off_t) throws {
        let fd = open(path + "/" + file.rawValue, O_RDWR | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600)
        try #require(fd >= 0)
        defer { close(fd) }
        try #require(ftruncate(fd, bytes) == 0)
    }
}
