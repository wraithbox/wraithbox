// C11-host-daemons, "Device set": every refused device kind, count and
// bundle file is refused with its rule, and the fixed set is accepted.
import Foundation
import Synchronization
import Testing

@testable import WraithBoxVM

/// Collects the check's log lines.
final class LogSink: Sendable {
    private let store = Mutex<[String]>([])

    var lines: [String] {
        store.withLock { $0 }
    }

    func append(_ line: String) {
        store.withLock { $0.append(line) }
    }
}

/// A VM bundle with every bundle file, in a new temporary directory.
final class BundleFixture {
    let path: String
    let outside: String

    init() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("wb-device-set-\(UUID().uuidString)")
        path = root.appendingPathComponent("macos-work.noindex").path
        outside = root.appendingPathComponent("outside").path
        try FileManager.default.createDirectory(atPath: path, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: outside, withIntermediateDirectories: true)
        for file in BundleFile.allCases {
            try write(file.rawValue)
        }
    }

    deinit {
        try? FileManager.default.removeItem(atPath: (path as NSString).deletingLastPathComponent)
    }

    func filePath(_ file: BundleFile) -> String {
        path + "/" + file.rawValue
    }

    func write(_ name: String, in directory: String? = nil) throws {
        let target = (directory ?? path) + "/" + name
        guard FileManager.default.createFile(atPath: target, contents: Data("x".utf8)) else {
            throw CocoaError(.fileWriteUnknown)
        }
    }

    func remove(_ file: BundleFile) throws {
        try FileManager.default.removeItem(atPath: filePath(file))
    }

    func check(
        _ description: VMDescription? = nil, slot: VMSlot = .work, set: DeviceSet? = nil,
        log: LogSink = LogSink()
    ) throws(DeviceSetRefusal) {
        let check = DeviceSetCheck(
            slot: slot, set: set ?? .fixed(for: slot), bundlePath: path, log: { log.append($0) })
        try check.check(description ?? .fixedSet(bundlePath: path, slot: slot))
    }
}

/// The fixed set of `slot` with `extra` added.
func fixedSet(_ bundle: BundleFixture, slot: VMSlot = .work, adding extra: [VMDevice])
    -> VMDescription
{
    var description = VMDescription.fixedSet(bundlePath: bundle.path, slot: slot)
    description.devices += extra
    return description
}

/// The fixed set of `slot` without its network devices.
func withoutNetwork(_ bundle: BundleFixture, slot: VMSlot) -> VMDescription {
    var description = VMDescription.fixedSet(bundlePath: bundle.path, slot: slot)
    description.devices.removeAll { $0 == .network(.fileHandle) }
    return description
}

/// The fixed set with the disk of `role` replaced.
func fixedSet(_ bundle: BundleFixture, replacing role: DiskRole, with disk: VMDisk) -> VMDescription
{
    var description = VMDescription.fixedSet(bundlePath: bundle.path, slot: .work)
    description.devices = description.devices.map { device in
        if case .disk(let old) = device, old.role == role {
            return .disk(disk)
        }
        return device
    }
    return description
}

@Suite("Device set: accepted")
struct DeviceSetAcceptedTests {
    @Test("the fixed set is accepted in every slot", arguments: VMSlot.allCases)
    func fixedSetAccepted(slot: VMSlot) throws {
        let bundle = try BundleFixture()
        let log = LogSink()
        try bundle.check(slot: slot, log: log)
        let devices = slot == .build ? 8 : 9
        #expect(
            log.lines == [
                "device-set: accepted slot=\(slot.rawValue) devices=\(devices) bundle=\(bundle.path)"
            ])
    }

    @Test(
        "the work and isolated sets have one file-handle network, the build set none",
        arguments: [(VMSlot.work, 1), (.isolated, 1), (.build, 0)])
    func networks(slot: VMSlot, count: Int) {
        #expect(DeviceSet.fixed(for: slot).fileHandleNetworks == count)
        let description = VMDescription.fixedSet(bundlePath: "/", slot: slot)
        #expect(description.devices.filter { $0 == .network(.fileHandle) }.count == count)
    }

    @Test("another file in the bundle is ignored")
    func otherFileIgnored() throws {
        let bundle = try BundleFixture()
        try bundle.write("cfg.json")
        try bundle.check()
    }

    @Test("a trailing slash on the bundle path is accepted")
    func trailingSlash() throws {
        let bundle = try BundleFixture()
        let check = DeviceSetCheck(slot: .work, bundlePath: bundle.path + "/", log: { _ in })
        try check.check(.fixedSet(bundlePath: bundle.path, slot: .work))
    }

}

@Suite("Device set: network devices")
struct DeviceSetNetworkTests {
    @Test(
        "a work or isolated VM without its network device is refused",
        arguments: [VMSlot.work, .isolated])
    func missingNetwork(slot: VMSlot) throws {
        let bundle = try BundleFixture()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(withoutNetwork(bundle, slot: slot), slot: slot)
        }
        #expect(error?.rule == .count)
    }

    @Test(
        "a second file-handle network device is refused in the work and isolated slots",
        arguments: [VMSlot.work, .isolated])
    func secondNetwork(slot: VMSlot) throws {
        let bundle = try BundleFixture()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(
                fixedSet(bundle, slot: slot, adding: [.network(.fileHandle)]), slot: slot)
        }
        #expect(error?.rule == .network)
    }

    @Test("the build slot still refuses a file-handle network device")
    func buildRefusesNetwork() throws {
        let bundle = try BundleFixture()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(
                fixedSet(bundle, slot: .build, adding: [.network(.fileHandle)]), slot: .build)
        }
        #expect(error?.rule == .network)
    }

    @Test(
        "every other attachment is refused in every slot, also in place of the file handle",
        arguments: VMSlot.allCases)
    func otherAttachments(slot: VMSlot) throws {
        let bundle = try BundleFixture()
        for attachment in [NetworkAttachment.nat, .bridged, .vmnet] {
            var descriptions = [fixedSet(bundle, slot: slot, adding: [.network(attachment)])]
            if slot != .build {
                descriptions.append(
                    VMDescription.fixedSet(bundlePath: bundle.path, slot: slot)
                        .replacingNetwork(with: attachment))
            }
            for description in descriptions {
                let error = #expect(throws: DeviceSetRefusal.self) {
                    try bundle.check(description, slot: slot)
                }
                #expect(error?.rule == .network, "\(attachment.rawValue) in \(slot.rawValue)")
            }
        }
    }
}

extension VMDescription {
    /// The description with each file-handle network device given
    /// `attachment` instead.
    func replacingNetwork(with attachment: NetworkAttachment) -> VMDescription {
        VMDescription(
            devices: devices.map { $0 == .network(.fileHandle) ? .network(attachment) : $0 })
    }
}

@Suite("Device set: refused kinds")
struct DeviceSetKindTests {
    @Test(
        "a refused device kind is refused with its rule",
        arguments: [
            (VMDevice.network(.nat), DeviceSetRule.network),
            (.network(.bridged), .network),
            (.network(.vmnet), .network),
            (.network(.fileHandle), .network),
            (.directoryShare(.singleDirectory), .share),
            (.directoryShare(.multipleDirectories), .share),
            (.directoryShare(.rosetta), .share),
            (.audioInput, .audioInput),
            (.console(.virtioConsole), .console),
            (.console(.spiceAgent), .console),
            (.console(.serialPortFile), .console),
            (.console(.serialPortFileHandle), .console),
            (.storage(.usbMassStorage), .storage),
            (.storage(.networkBlockDevice), .storage),
            (.storage(.hostBlockDevice), .storage),
            (.unknown("memory-balloon"), .unknown),
        ])
    func refusedKind(device: VMDevice, rule: DeviceSetRule) throws {
        let bundle = try BundleFixture()
        let log = LogSink()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, adding: [device]), log: log)
        }
        #expect(error?.rule == rule)
        #expect(log.lines.count == 1)
        #expect(
            log.lines.first?.hasPrefix(
                "device-set: refused slot=work rule=\(rule.rawValue) detail=") == true)
    }

    @Test("a NAT network is refused in every slot", arguments: VMSlot.allCases)
    func natEverySlot(slot: VMSlot) throws {
        let bundle = try BundleFixture()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, adding: [.network(.nat)]), slot: slot)
        }
        #expect(error?.rule == .network)
    }

    @Test("a NAT network is refused where a set allows a network device")
    func natWhereNetworkAllowed() throws {
        let bundle = try BundleFixture()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(
                fixedSet(bundle, adding: [.network(.nat)]), set: DeviceSet(fileHandleNetworks: 1))
        }
        #expect(error?.rule == .network)
    }

    @Test("a second network device is refused where a set allows one")
    func secondNetwork() throws {
        let bundle = try BundleFixture()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(
                fixedSet(bundle, adding: [.network(.fileHandle), .network(.fileHandle)]),
                set: DeviceSet(fileHandleNetworks: 1))
        }
        #expect(error?.rule == .network)
    }

    @Test("a device kind is refused before the bundle is read")
    func kindBeforeBundle() throws {
        let bundle = try BundleFixture()
        try bundle.remove(.systemDisk)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, adding: [.network(.nat)]))
        }
        #expect(error?.rule == .network)
    }
}

@Suite("Device set: counts")
struct DeviceSetCountTests {
    @Test(
        "a second device of the set is refused",
        arguments: [
            VMDevice.vsock, .entropy, .macGraphics(displays: 1), .usbKeyboard, .usbScreenPointer,
        ])
    func secondDevice(device: VMDevice) throws {
        let bundle = try BundleFixture()
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, adding: [device]))
        }
        #expect(error?.rule == .count)
    }

    @Test("a disk beyond the three is refused", arguments: DiskRole.allCases)
    func extraDisk(role: DiskRole) throws {
        let bundle = try BundleFixture()
        let disk = VMDisk(role: role, path: bundle.filePath(role.file), readOnly: role.readOnly)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, adding: [.disk(disk)]))
        }
        #expect(error?.rule == .count)
    }

    @Test("a missing device of the set is refused")
    func missingDevice() throws {
        let bundle = try BundleFixture()
        var description = VMDescription.fixedSet(bundlePath: bundle.path, slot: .work)
        description.devices.removeAll { $0 == .entropy }
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(description)
        }
        #expect(error?.rule == .count)
    }

    @Test("a graphics device without exactly one display is refused", arguments: [0, 2])
    func displays(count: Int) throws {
        let bundle = try BundleFixture()
        var description = VMDescription.fixedSet(bundlePath: bundle.path, slot: .work)
        description.devices = description.devices.map {
            $0 == .macGraphics(displays: 1) ? .macGraphics(displays: count) : $0
        }
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(description)
        }
        #expect(error?.rule == .count)
    }
}

@Suite("Device set: bundle files")
struct DeviceSetBundleTests {
    @Test("a missing bundle file is refused", arguments: BundleFile.allCases)
    func missingFile(file: BundleFile) throws {
        let bundle = try BundleFixture()
        try bundle.remove(file)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check()
        }
        #expect(error?.rule == .bundleFileMissing)
    }

    @Test("a missing bundle is refused")
    func missingBundle() throws {
        let bundle = try BundleFixture()
        try FileManager.default.removeItem(atPath: bundle.path)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check()
        }
        #expect(error?.rule == .bundleFileMissing)
    }

    @Test("a symbolic link to a file outside the bundle is refused", arguments: BundleFile.allCases)
    func linkOutside(file: BundleFile) throws {
        let bundle = try BundleFixture()
        try bundle.remove(file)
        try bundle.write(file.rawValue, in: bundle.outside)
        try FileManager.default.createSymbolicLink(
            atPath: bundle.filePath(file), withDestinationPath: bundle.outside + "/" + file.rawValue
        )
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check()
        }
        #expect(error?.rule == .fileOutsideBundle)
    }

    @Test("a symbolic link to a file inside the bundle is refused")
    func linkInside() throws {
        let bundle = try BundleFixture()
        try bundle.write("other.img")
        try bundle.remove(.systemDisk)
        try FileManager.default.createSymbolicLink(
            atPath: bundle.filePath(.systemDisk), withDestinationPath: "other.img")
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check()
        }
        #expect(error?.rule == .fileOutsideBundle)
    }

    @Test("a directory at a bundle file's name is refused")
    func directory() throws {
        let bundle = try BundleFixture()
        try bundle.remove(.dataDisk)
        try FileManager.default.createDirectory(
            atPath: bundle.filePath(.dataDisk), withIntermediateDirectories: false)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check()
        }
        #expect(error?.rule == .fileOutsideBundle)
    }

    @Test("a hard link to a file outside the bundle is refused")
    func hardLink() throws {
        let bundle = try BundleFixture()
        try bundle.write(BundleFile.systemDisk.rawValue, in: bundle.outside)
        try bundle.remove(.systemDisk)
        try FileManager.default.linkItem(
            atPath: bundle.outside + "/" + BundleFile.systemDisk.rawValue,
            toPath: bundle.filePath(.systemDisk))
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check()
        }
        #expect(error?.rule == .fileOutsideBundle)
    }
}

@Suite("Device set: disks")
struct DeviceSetDiskTests {
    @Test("a disk file outside the bundle is refused", arguments: DiskRole.allCases)
    func diskOutside(role: DiskRole) throws {
        let bundle = try BundleFixture()
        try bundle.write(role.file.rawValue, in: bundle.outside)
        let disk = VMDisk(
            role: role, path: bundle.outside + "/" + role.file.rawValue, readOnly: role.readOnly)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, replacing: role, with: disk))
        }
        #expect(error?.rule == .fileOutsideBundle)
    }

    @Test("a disk file that doesn't exist outside the bundle is refused")
    func diskNowhere() throws {
        let bundle = try BundleFixture()
        let disk = VMDisk(role: .data, path: bundle.outside + "/none.img", readOnly: false)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, replacing: .data, with: disk))
        }
        #expect(error?.rule == .fileOutsideBundle)
    }

    @Test("a disk path that leaves the bundle through .. is refused")
    func diskDotDot() throws {
        let bundle = try BundleFixture()
        try bundle.write(BundleFile.systemDisk.rawValue, in: bundle.outside)
        let disk = VMDisk(
            role: .system, path: bundle.path + "/../outside/" + BundleFile.systemDisk.rawValue,
            readOnly: false)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, replacing: .system, with: disk))
        }
        #expect(error?.rule == .fileOutsideBundle)
    }

    @Test("a disk on another file in the bundle is refused")
    func diskOtherFile() throws {
        let bundle = try BundleFixture()
        let disk = VMDisk(role: .system, path: bundle.filePath(.dataDisk), readOnly: false)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, replacing: .system, with: disk))
        }
        #expect(error?.rule == .disk)
    }

    @Test("a disk with the wrong access mode is refused", arguments: DiskRole.allCases)
    func accessMode(role: DiskRole) throws {
        let bundle = try BundleFixture()
        let disk = VMDisk(role: role, path: bundle.filePath(role.file), readOnly: !role.readOnly)
        let error = #expect(throws: DeviceSetRefusal.self) {
            try bundle.check(fixedSet(bundle, replacing: role, with: disk))
        }
        #expect(error?.rule == .disk)
    }
}
