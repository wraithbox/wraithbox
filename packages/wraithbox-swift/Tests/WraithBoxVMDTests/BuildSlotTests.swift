// C11-host-daemons, "Slot" and "Start interface": the build slot of
// wb-vmd-install. Its bundle and restore image checks run before
// confinement, its profile alone has the install rules, and only it may
// hold descriptor 5, the build password pipe (S04-10-vmd-install,
// "`wb-vmd-install`", "The restore image"; S04-03-self-sandboxing).
import Darwin
import Foundation
import Testing
import WraithBoxVM

@testable import WraithBoxVMD

@Suite("Build slot")
struct BuildSlotTests {
    /// A build bundle and a restore image at the fixed path.
    private func buildFixture() throws -> BundleFixture {
        let fixture = try BundleFixture(slot: .build, files: [])
        FileManager.default.createFile(atPath: fixture.restoreImagePath, contents: Data("i".utf8))
        return fixture
    }

    @Test("the build slot resolves to the build bundle and the restore image's real path")
    func resolves() throws {
        let fixture = try buildFixture()
        let resolved = try SlotResolution.resolve(slotValue: "build", home: fixture.home.path)
        #expect(resolved.slot == .build)
        #expect(resolved.realBundle == fixture.path)
        #expect(resolved.realRestoreImage == fixture.restoreImagePath)
    }

    @Test("the work and isolated slots get no restore image path")
    func noRestoreImageElsewhere() throws {
        let fixture = try BundleFixture()
        FileManager.default.createFile(atPath: fixture.restoreImagePath, contents: Data("i".utf8))
        let resolved = try SlotResolution.resolve(slotValue: "work", home: fixture.home.path)
        #expect(resolved.realRestoreImage == nil)
    }

    @Test("a missing restore image is refused with vmd-restore-image-mismatch")
    func missing() throws {
        let fixture = try BundleFixture(slot: .build, files: [])
        try expectRefusal(.restoreImageMismatch) {
            try SlotResolution.resolve(slotValue: "build", home: fixture.home.path)
        }
    }

    @Test("a restore image that is a directory is refused with vmd-restore-image-mismatch")
    func directory() throws {
        let fixture = try BundleFixture(slot: .build, files: [])
        try FileManager.default.createDirectory(
            atPath: fixture.restoreImagePath, withIntermediateDirectories: false)
        try expectRefusal(.restoreImageMismatch) {
            try SlotResolution.resolve(slotValue: "build", home: fixture.home.path)
        }
    }

    @Test("a restore image that is a symbolic link is refused with vmd-restore-image-mismatch")
    func symbolicLink() throws {
        let fixture = try BundleFixture(slot: .build, files: [])
        let elsewhere = try TemporaryDirectory()
        let target = elsewhere.path + "/other.ipsw"
        FileManager.default.createFile(atPath: target, contents: Data("i".utf8))
        try FileManager.default.createSymbolicLink(
            atPath: fixture.restoreImagePath, withDestinationPath: target)
        try expectRefusal(.restoreImageMismatch) {
            try SlotResolution.resolve(slotValue: "build", home: fixture.home.path)
        }
    }

    @Test("a restore image that is a hard link is refused with vmd-restore-image-mismatch")
    func hardLink() throws {
        let fixture = try BundleFixture(slot: .build, files: [])
        let elsewhere = try TemporaryDirectory()
        let target = elsewhere.path + "/other.ipsw"
        FileManager.default.createFile(atPath: target, contents: Data("i".utf8))
        try FileManager.default.linkItem(atPath: target, toPath: fixture.restoreImagePath)
        try expectRefusal(.restoreImageMismatch) {
            try SlotResolution.resolve(slotValue: "build", home: fixture.home.path)
        }
    }

    @Test("the build profile adds the three install rules, and no other slot's profile has them")
    func profiles() {
        #expect(Confinement.profile(for: .build) == Confinement.profile + Confinement.installRules)
        for rule in [
            #"(allow file-read* (literal (param "IPSW")))"#,
            #"(allow mach-lookup (xpc-service-name "com.apple.Virtualization.Installation"))"#,
            #"(require-all (literal (param "IPSW")) (extension-class "com.apple.app-sandbox.read"))"#,
        ] {
            #expect(Confinement.installRules.contains(rule))
        }
        for slot in [VMSlot.work, .isolated, .layer] {
            let text = Confinement.profile(for: slot)
            #expect(text == Confinement.profile)
            #expect(!text.contains("IPSW"))
            #expect(!text.contains("Installation"))
        }
    }

    @Test("only the build slot passes IPSW, and the build slot refuses to confine without it")
    func parameters() throws {
        let build = ResolvedSlot(
            slot: .build, bundlePath: "/b", realBundle: "/b", realRestoreImage: "/r.ipsw")
        #expect(
            try Confinement.parameters(build, cache: "/c").map(\.0) == ["BUNDLE", "CACHE", "IPSW"])
        let work = ResolvedSlot(
            slot: .work, bundlePath: "/w", realBundle: "/w", realRestoreImage: "/r.ipsw")
        #expect(try Confinement.parameters(work, cache: "/c").map(\.0) == ["BUNDLE", "CACHE"])
        let withoutImage = ResolvedSlot(
            slot: .build, bundlePath: "/b", realBundle: "/b", realRestoreImage: nil)
        try expectRefusal(.sandboxParameter) {
            try Confinement.parameters(withoutImage, cache: "/c")
        }
    }

    @Test("descriptor 5 is accepted as a pipe in the build slot and closed in every other slot")
    func provisioningDescriptorAccepted() throws {
        let pipeEnds = try makePipe()
        defer { closeAll([pipeEnds.0, pipeEnds.1]) }
        try StartInterface.checkProvisioning(slot: .build, fd: pipeEnds.0)
        // Far above any descriptor the test process opens.
        for slot in [VMSlot.work, .isolated, .layer] {
            try StartInterface.checkProvisioning(slot: slot, fd: 9999)
        }
    }

    @Test("an open descriptor 5 outside the build slot is refused with vmd-descriptor-invalid")
    func provisioningDescriptorOutsideBuild() throws {
        let pipeEnds = try makePipe()
        defer { closeAll([pipeEnds.0, pipeEnds.1]) }
        for slot in [VMSlot.work, .isolated, .layer] {
            try expectRefusal(.descriptor) {
                try StartInterface.checkProvisioning(slot: slot, fd: pipeEnds.0)
            }
        }
    }

    @Test("a missing descriptor 5, or one that isn't a pipe, is refused in the build slot")
    func provisioningDescriptorNotPipe() throws {
        let stream = try makeSocketPair(SOCK_STREAM)
        let file = open("/dev/null", O_RDONLY)
        defer { closeAll([stream.0, stream.1, file]) }
        for fd in [stream.0, file, 9999] {
            try expectRefusal(.descriptor) {
                try StartInterface.checkProvisioning(slot: .build, fd: fd)
            }
        }
    }
}
