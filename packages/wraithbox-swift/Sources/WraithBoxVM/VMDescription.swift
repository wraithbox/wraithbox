// wb-vmd's own description of a VM configuration. `DeviceSetCheck` checks
// it before any Virtualization object exists (S04-02-vm-provider, "The
// device set is fixed and checked"). The type names no Virtualization
// class, and it can express every device the check refuses, so tests can
// hand the check a refused device. Device configuration never comes from
// a request or from the bundle.

/// A VM configuration, as `wb-vmd` describes it to itself.
public struct VMDescription: Sendable, Equatable {
    public var devices: [VMDevice]

    public init(devices: [VMDevice]) {
        self.devices = devices
    }

    /// The fixed device set of S06-06-devices-and-warm-start, "Devices",
    /// for `slot`, with each disk at its role's file in the bundle, and as
    /// many file-handle network devices as the slot's set has.
    public static func fixedSet(bundlePath: String, slot: VMSlot) -> VMDescription {
        let bundle = VMBundle(path: bundlePath)
        let networks = DeviceSet.fixed(for: slot).fileHandleNetworks
        let network = [VMDevice](repeating: .network(.fileHandle), count: networks)
        return VMDescription(
            devices: [
                .vsock,
                .entropy,
                .disk(VMDisk(role: .system, path: bundle.path(of: .systemDisk), readOnly: false)),
                .disk(VMDisk(role: .data, path: bundle.path(of: .dataDisk), readOnly: false)),
                .disk(VMDisk(role: .tools, path: bundle.path(of: .toolsDisk), readOnly: true)),
                .macGraphics(displays: 1),
                .usbKeyboard,
                .usbScreenPointer,
            ] + network)
    }
}

/// One device of a VM. The allowed kinds come first, then the refused
/// ones. A switch over this enum has no default case, so a new case
/// doesn't compile until the check handles it.
public enum VMDevice: Sendable, Equatable {
    /// The host-guest socket device.
    case vsock
    /// The virtio entropy device.
    case entropy
    /// A virtio block device on a disk image file.
    case disk(VMDisk)
    /// The Mac graphics device with this many displays.
    case macGraphics(displays: Int)
    /// A USB keyboard.
    case usbKeyboard
    /// A USB screen-coordinate pointing device.
    case usbScreenPointer
    /// A virtio network device with this attachment.
    case network(NetworkAttachment)
    /// A virtio file system device sharing host directories.
    case directoryShare(DirectoryShare)
    /// A sound device with an input stream from the host.
    case audioInput
    /// A console or serial port device.
    case console(ConsoleKind)
    /// A storage device other than a disk image file on a virtio block
    /// device.
    case storage(StorageKind)
    /// A device kind with no case of its own, named for the log: a memory
    /// balloon, sound output, another graphics, keyboard or pointing
    /// device, USB passthrough, or a custom virtio device.
    case unknown(String)
}

/// What a network device's packets go to.
public enum NetworkAttachment: String, Sendable, Equatable {
    /// A socket `wb-vmd` holds, leading to `wb-netd`.
    case fileHandle = "file-handle"
    /// The Virtualization service's own NAT.
    case nat
    /// A bridge to a host interface.
    case bridged
    /// A vmnet network.
    case vmnet
}

/// The kind of a directory share.
public enum DirectoryShare: String, Sendable, Equatable {
    case singleDirectory = "single-directory"
    case multipleDirectories = "multiple-directories"
    case rosetta
}

/// The kind of a console or serial port device.
public enum ConsoleKind: String, Sendable, Equatable {
    case virtioConsole = "virtio-console"
    case spiceAgent = "spice-agent"
    case serialPortFile = "serial-port-file"
    case serialPortFileHandle = "serial-port-file-handle"
}

/// The kind of a storage device the set doesn't allow.
public enum StorageKind: String, Sendable, Equatable {
    case usbMassStorage = "usb-mass-storage"
    case networkBlockDevice = "network-block-device"
    case hostBlockDevice = "host-block-device"
}

/// A disk of the fixed set.
public struct VMDisk: Sendable, Equatable {
    public var role: DiskRole
    /// The disk image file `wb-vmd` would attach.
    public var path: String
    public var readOnly: Bool

    public init(role: DiskRole, path: String, readOnly: Bool) {
        self.role = role
        self.path = path
        self.readOnly = readOnly
    }
}

/// The disks of S06-06-devices-and-warm-start, "Devices".
public enum DiskRole: String, Sendable, Equatable, CaseIterable {
    case system
    case data
    case tools

    /// The bundle file the disk is attached from (S06-05-disks, "Bundle
    /// files").
    public var file: BundleFile {
        switch self {
        case .system: .systemDisk
        case .data: .dataDisk
        case .tools: .toolsDisk
        }
    }

    /// The guest tools disk is attached read-only (S06-12-guest-agent,
    /// "Guest tools disk"). The others are writable.
    public var readOnly: Bool {
        self == .tools
    }
}
