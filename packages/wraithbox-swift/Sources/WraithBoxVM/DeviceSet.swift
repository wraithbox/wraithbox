// The fixed device set of each `wb-vmd` slot, and the rules the check
// refuses with (S04-02-vm-provider, "The device set is fixed and
// checked"; S06-06-devices-and-warm-start, "Devices").

/// The slot a `wb-vmd` instance runs in (S04-04-vm-processes, "The slot").
public enum VMSlot: String, Sendable, Equatable, CaseIterable {
    case work
    case isolated
    case build
}

/// What a slot's VM has beyond the devices every VM has exactly one of: a
/// vsock device, an entropy device, one disk of each role, the Mac
/// graphics device with one display, a USB keyboard and a USB pointer.
public struct DeviceSet: Sendable, Equatable {
    /// How many network devices with a file-handle attachment the VM has:
    /// exactly this many. No other network attachment is ever allowed.
    public let fileHandleNetworks: Int

    /// The set for a slot. The work and isolated VMs have one network
    /// device, whose file-handle attachment leads to `wb-netd`
    /// (S07-02-packet-path, "Packet transport"), the guest's only network
    /// path (SEC05-default-deny). The build VM has none until its build
    /// network comes with V1-M7-image-build.
    public static func fixed(for slot: VMSlot) -> DeviceSet {
        switch slot {
        case .work, .isolated:
            DeviceSet(fileHandleNetworks: 1)
        case .build:
            DeviceSet(fileHandleNetworks: 0)
        }
    }
}

/// The rule a refusal names, in the log and in the gRPC error.
public enum DeviceSetRule: String, Sendable, Equatable, CaseIterable {
    /// A network device the slot's set doesn't allow, or more of them than
    /// it has (SEC05-default-deny).
    case network = "device-set-network"
    /// A directory or Rosetta share (SEC02-no-host-fs-share).
    case share = "device-set-share"
    /// A sound device with host audio input.
    case audioInput = "device-set-audio-input"
    /// A console, SPICE agent or serial port device.
    case console = "device-set-console"
    /// USB mass storage, a network block device or a host block device.
    case storage = "device-set-storage"
    /// A device of the set missing or present more than once.
    case count = "device-set-count"
    /// A device kind the check doesn't know.
    case unknown = "device-set-unknown"
    /// A disk whose file or access mode isn't its role's.
    case disk = "device-set-disk"
    /// A bundle file that isn't a regular file with one link inside the
    /// bundle, or a disk file outside the bundle.
    case fileOutsideBundle = "device-set-file-outside-bundle"
    /// A bundle file, or the bundle, that can't be found.
    case bundleFileMissing = "bundle-file-missing"
}

/// Why the check refused a VM description.
public struct DeviceSetRefusal: Error, Sendable, Equatable, CustomStringConvertible {
    public let rule: DeviceSetRule
    /// A short detail for the log and the gRPC error.
    public let detail: String

    public init(_ rule: DeviceSetRule, _ detail: String) {
        self.rule = rule
        self.detail = detail
    }

    public var description: String {
        "\(rule.rawValue): \(detail)"
    }
}
