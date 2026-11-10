// C11-host-daemons, "VMService": Install and the provisioning start, with
// a fake engine. Only the build slot installs, and every refusal before
// the restore image is read leaves the bundle as it was
// (S04-10-vmd-install, "`wb-vmd-install`", "The build bundle").
import Foundation
import GRPCCore
import Synchronization
import Testing
import WraithBoxRPC
import WraithBoxVM

@testable import WraithBoxVMD

/// The files wb-hostd puts in a new build bundle before the install.
private let hostMadeFiles: [BundleFile] = [.dataDisk, .toolsDisk]

/// The messages of an Install stream, kept when the call fails.
private final class InstallStream: Sendable {
    private let store = Mutex<[Wraithbox_Vmd_V1_InstallResponse]>([])

    var messages: [Wraithbox_Vmd_V1_InstallResponse] {
        store.withLock { $0 }
    }

    var percents: [UInt32] {
        messages.filter { !$0.hasError }.map(\.percent)
    }

    /// Calls Install and keeps each message.
    func install(
        _ fixture: ServiceFixture, cpuCount: UInt32 = 4, memoryMiB: UInt64 = 4096
    ) async throws {
        var request = Wraithbox_Vmd_V1_InstallRequest()
        request.cpuCount = cpuCount
        request.memoryMib = memoryMiB
        try await fixture.withClient { client in
            try await client.install(request) { response in
                for try await message in response.messages {
                    self.store.withLock { $0.append(message) }
                }
            }
        }
    }
}

@Suite("Install")
struct InstallTests {
    @Test(
        "Install outside the build slot is refused with vmd-install-refused and creates no file",
        arguments: [VMSlot.work, .isolated, .layer])
    func refusedSlot(slot: VMSlot) async throws {
        let fixture = try await ServiceFixture(slot: slot, files: [])
        let stream = InstallStream()
        let (code, reason) = await refusal { try await stream.install(fixture) }
        #expect(code == .failedPrecondition)
        #expect(reason == "vmd-install-refused")
        #expect(fixture.recorded.rules == ["vmd-install-refused"])
        #expect(try FileManager.default.contentsOfDirectory(atPath: fixture.bundlePath).isEmpty)
        #expect(await fixture.engine.restoreImageReads.isEmpty)
        #expect(stream.messages.isEmpty)
    }

    @Test(
        "a build bundle that holds a file the install creates is refused with vmd-install-refused",
        arguments: BuildInstaller.createdFiles)
    func bundleNotEmpty(file: BundleFile) async throws {
        let fixture = try await ServiceFixture(slot: .build, files: hostMadeFiles + [file])
        let before = try names(fixture)
        let (_, reason) = await refusal { try await InstallStream().install(fixture) }
        #expect(reason == "vmd-install-refused")
        #expect(try names(fixture) == before)
        #expect(await fixture.engine.restoreImageReads.isEmpty)
    }

    @Test("Install creates the bundle files, reports rising percents to 100, and runs once")
    func installs() async throws {
        let fixture = try await ServiceFixture(slot: .build, files: hostMadeFiles)
        let stream = InstallStream()
        try await stream.install(fixture)
        #expect(stream.percents == [0, 1, 50, 99, 100])
        #expect(try names(fixture) == BundleFile.allCases.map(\.rawValue).sorted())
        let bundle = VMBundle(path: fixture.bundlePath)
        #expect(try contents(bundle.path(of: .hardwareModel)) == [0x68, 0x77])
        #expect(
            try contents(bundle.path(of: .machineIdentifier)) == fixture.engine.machineIdentifier)
        let system = try FileManager.default.attributesOfItem(atPath: bundle.path(of: .systemDisk))
        #expect((system[.size] as? Int) == 64 << 30)
        let (request, imagePath) = try #require(await fixture.engine.installs.first)
        #expect(
            request.description
                == VMDescription.fixedSet(bundlePath: fixture.bundlePath, slot: .build))
        #expect(request.networks == 0)
        #expect(imagePath == HostPaths.data(home: fixture.homePath) + "/" + HostPaths.restoreImage)
        #expect(await fixture.engine.restoreImageReads == [imagePath])

        let (_, reason) = await refusal { try await InstallStream().install(fixture) }
        #expect(reason == "vmd-install-refused")
        #expect(await fixture.engine.installs.count == 1)
    }

    @Test("Start in the build slot boots the build slot's fixed set, without a network device")
    func startBuild() async throws {
        let fixture = try await ServiceFixture(slot: .build)
        try await fixture.withClient { client in
            _ = try await client.start(startRequest(generation: 1))
        }
        let request = try #require(await fixture.engine.requests.first)
        #expect(
            request.description
                == VMDescription.fixedSet(bundlePath: fixture.bundlePath, slot: .build))
        #expect(request.networks == 0)
    }

    @Test("Install while the build VM runs is refused with vmd-install-refused")
    func whileRunning() async throws {
        let fixture = try await ServiceFixture(slot: .build)
        try await fixture.supervisor.start(
            generation: 1, cpuCount: 4, memoryMiB: 4096, macAddress: testMAC, provisioning: false)
        do {
            try await fixture.supervisor.install(cpuCount: 4, memoryMiB: 4096) { _ in }
            Issue.record("the install was not refused")
        } catch {
            #expect(error.rule == "vmd-install-refused")
            #expect(error.detail == "the VM is running")
        }
        #expect(await fixture.engine.restoreImageReads.isEmpty)
    }

    @Test("resources outside the host's range are refused with vm-resources before any file")
    func resources() async throws {
        let fixture = try await ServiceFixture(slot: .build, files: hostMadeFiles)
        let before = try names(fixture)
        let (_, reason) = await refusal {
            try await InstallStream().install(fixture, cpuCount: 0)
        }
        #expect(reason == "vm-resources")
        #expect(try names(fixture) == before)
    }

    @Test("an image this host can't run is refused with vmd-restore-image-unsupported")
    func unsupported() async throws {
        let fixture = try await ServiceFixture(slot: .build, files: hostMadeFiles)
        let before = try names(fixture)
        await MainActor.run {
            fixture.engine.restoreImage = RestoreImageInfo(buildVersion: "99Z1", hardwareModel: nil)
        }
        let (_, reason) = await refusal { try await InstallStream().install(fixture) }
        #expect(reason == "vmd-restore-image-unsupported")
        #expect(try names(fixture) == before)
        #expect(await fixture.engine.installs.isEmpty)
    }

    @Test("a framework failure ends the stream with its VMError and vm-install-failed")
    func frameworkFailure() async throws {
        let fixture = try await ServiceFixture(slot: .build, files: hostMadeFiles)
        await MainActor.run {
            fixture.engine.installFailure = VMFailure(
                code: 10001, failureReason: "the reason", errno: 5, description: "install failed")
        }
        let stream = InstallStream()
        let (code, reason) = await refusal { try await stream.install(fixture) }
        #expect(code == .failedPrecondition)
        #expect(reason == "vm-install-failed")
        let last = try #require(stream.messages.last)
        #expect(last.hasError)
        #expect(last.error.code == 10001)
        #expect(last.error.failureReason == "the reason")
        #expect(last.error.errno == 5)
        #expect(last.error.description_p == "install failed")
        #expect(!stream.percents.contains(100))
    }

    @Test(
        "a Start with provisioning is refused with vmd-provisioning-refused in every slot",
        arguments: VMSlot.allCases)
    func provisioningRefused(slot: VMSlot) async throws {
        let fixture = try await ServiceFixture(slot: slot)
        var request = startRequest(generation: 1)
        request.provisioning = true
        let (code, reason) = await refusal {
            try await fixture.withClient { client in
                _ = try await client.start(request)
            }
        }
        #expect(code == .permissionDenied)
        #expect(reason == "vmd-provisioning-refused")
        #expect(fixture.recorded.rules == ["vmd-provisioning-refused"])
        #expect(await fixture.engine.requests.isEmpty)
    }

    @Test("the reported percent rises by whole percents and stays below 100 until the end")
    func percents() {
        var percent = InstallPercent()
        let fractions = [-1, 0, 0.005, .nan, 0.01, 0.42, 0.42, 0.1, 0.995, 1, .infinity, 7e300]
        let reported = fractions.compactMap { percent.next($0) }
        #expect(reported == [0, 1, 42, 99])
    }

    private func names(_ fixture: ServiceFixture) throws -> [String] {
        try FileManager.default.contentsOfDirectory(atPath: fixture.bundlePath).sorted()
    }

    private func contents(_ path: String) throws -> [UInt8] {
        Array(try Data(contentsOf: URL(fileURLWithPath: path)))
    }
}
