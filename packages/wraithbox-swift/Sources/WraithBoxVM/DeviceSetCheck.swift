// The device-set check (S04-02-vm-provider, "The device set is fixed and
// checked"). `wb-vmd` runs it before every start and restore, before it
// creates any Virtualization object. Only this code keeps a NAT network
// from the guest, because the sandbox profile can't refuse one
// (X25-vmd-sandbox). It enforces SEC02-no-host-fs-share and keeps every
// network path outside `wb-netd` away (SEC05-default-deny).

/// Checks a VM description against its slot's fixed device set and the
/// files of its bundle. Every decision is logged with its rule.
public struct DeviceSetCheck: Sendable {
    public let slot: VMSlot
    public let bundle: VMBundle
    let set: DeviceSet
    let log: @Sendable (String) -> Void

    /// A check for a slot's fixed set, on the bundle at the slot's path.
    public init(slot: VMSlot, bundlePath: String, log: @escaping @Sendable (String) -> Void) {
        self.init(slot: slot, set: .fixed(for: slot), bundlePath: bundlePath, log: log)
    }

    init(
        slot: VMSlot, set: DeviceSet, bundlePath: String, log: @escaping @Sendable (String) -> Void
    ) {
        self.slot = slot
        self.set = set
        self.bundle = VMBundle(path: bundlePath)
        self.log = log
    }

    /// Accepts the description, or throws the first refusal. The device
    /// kinds and counts are checked before any bundle file is touched, and
    /// the bundle files before the disks that name them.
    public func check(_ description: VMDescription) throws(DeviceSetRefusal) {
        do throws(DeviceSetRefusal) {
            try Self.checkKinds(description.devices)
            try checkCounts(description.devices)
            let realBundle = try bundle.checkFiles()
            try checkDisks(description.devices, realBundle: realBundle)
        } catch {
            log(
                "device-set: refused slot=\(slot.rawValue) rule=\(error.rule.rawValue) detail=\(error.detail)"
            )
            throw error
        }
        log(
            "device-set: accepted slot=\(slot.rawValue) devices=\(description.devices.count) bundle=\(bundle.path)"
        )
    }

    private static func checkKinds(_ devices: [VMDevice]) throws(DeviceSetRefusal) {
        for device in devices {
            if let refusal = kindRefusal(device) {
                throw refusal
            }
        }
    }

    /// The refusal for a device kind that is never in any slot's set, or
    /// nil for a kind a set can have. The counts check those next.
    static func kindRefusal(_ device: VMDevice) -> DeviceSetRefusal? {
        switch device {
        case .vsock, .entropy, .disk, .macGraphics, .usbKeyboard, .usbScreenPointer,
            .network(.fileHandle):
            return nil
        case .network(let attachment):
            // SEC05-default-deny: a NAT, bridged or vmnet network bypasses
            // wb-netd and wb-proxyd.
            return DeviceSetRefusal(.network, "\(attachment.rawValue) network device")
        case .directoryShare(let share):
            // SEC02-no-host-fs-share: no host directory reaches the guest.
            return DeviceSetRefusal(.share, "\(share.rawValue) share")
        case .audioInput:
            return DeviceSetRefusal(.audioInput, "sound device with host audio input")
        case .console(let kind):
            return DeviceSetRefusal(.console, "\(kind.rawValue) device")
        case .storage(let kind):
            return DeviceSetRefusal(.storage, "\(kind.rawValue) device")
        case .unknown(let name):
            return DeviceSetRefusal(.unknown, "unknown device kind \(name)")
        }
    }

    private func checkCounts(_ devices: [VMDevice]) throws(DeviceSetRefusal) {
        var counts: [SetMember: Int] = [:]
        var networks = 0
        for device in devices {
            if case .network = device {
                networks += 1
            }
            if let member = SetMember(device) {
                counts[member, default: 0] += 1
            }
        }
        // SEC05-default-deny: no slot has more network devices than its
        // set has. A missing one is a missing device of the set.
        guard networks <= set.fileHandleNetworks else {
            throw DeviceSetRefusal(
                .network,
                "\(networks) network devices, the \(slot.rawValue) slot allows \(set.fileHandleNetworks)"
            )
        }
        guard networks == set.fileHandleNetworks else {
            throw DeviceSetRefusal(
                .count,
                "\(networks) network devices, the \(slot.rawValue) slot has \(set.fileHandleNetworks)"
            )
        }
        for member in SetMember.allCases where counts[member, default: 0] != 1 {
            throw DeviceSetRefusal(
                .count, "\(counts[member, default: 0]) \(member.rawValue) devices, the set has 1")
        }
        for case .macGraphics(let displays) in devices where displays != 1 {
            throw DeviceSetRefusal(
                .count, "Mac graphics device with \(displays) displays, the set has 1")
        }
    }

    /// Each disk must be its role's bundle file, with its role's access
    /// mode. The bundle files are already checked.
    private func checkDisks(_ devices: [VMDevice], realBundle: String) throws(DeviceSetRefusal) {
        for case .disk(let disk) in devices {
            let file = disk.role.file
            guard disk.path == bundle.path(of: file) else {
                throw Self.diskPathRefusal(disk, realBundle: realBundle)
            }
            guard disk.readOnly == disk.role.readOnly else {
                let mode = disk.role.readOnly ? "read-only" : "writable"
                throw DeviceSetRefusal(.disk, "\(disk.role.rawValue) disk must be \(mode)")
            }
        }
    }

    private static func diskPathRefusal(_ disk: VMDisk, realBundle: String) -> DeviceSetRefusal {
        // A path that doesn't resolve counts as outside the bundle.
        guard let real = realPath(disk.path), real.hasPrefix(realBundle + "/") else {
            return DeviceSetRefusal(
                .fileOutsideBundle,
                "\(disk.role.rawValue) disk \(disk.path) is outside \(realBundle)")
        }
        return DeviceSetRefusal(
            .disk, "\(disk.role.rawValue) disk is \(disk.path), not \(disk.role.file.rawValue)")
    }
}

/// The devices every VM has exactly one of.
enum SetMember: String, CaseIterable {
    case vsock
    case entropy
    case systemDisk = "system disk"
    case dataDisk = "data disk"
    case toolsDisk = "tools disk"
    case macGraphics = "Mac graphics"
    case usbKeyboard = "USB keyboard"
    case usbScreenPointer = "USB pointer"

    init?(_ device: VMDevice) {
        switch device {
        case .vsock: self = .vsock
        case .entropy: self = .entropy
        case .disk(let disk): self = Self.member(of: disk.role)
        case .macGraphics: self = .macGraphics
        case .usbKeyboard: self = .usbKeyboard
        case .usbScreenPointer: self = .usbScreenPointer
        case .network, .directoryShare, .audioInput, .console, .storage, .unknown: return nil
        }
    }

    private static func member(of role: DiskRole) -> SetMember {
        switch role {
        case .system: .systemDisk
        case .data: .dataDisk
        case .tools: .toolsDisk
        }
    }
}
