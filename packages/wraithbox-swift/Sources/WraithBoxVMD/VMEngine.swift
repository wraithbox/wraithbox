// The seam between wb-vmd's VM logic and the Virtualization framework.
// VMSupervisor holds the rules (one VM, generations, resources, the
// device-set check), and a VMEngine only builds and runs what the
// supervisor already checked. Tests use a fake engine.
import WraithBoxVM

/// A VM's state, as VZVirtualMachine.State has it.
public enum VMRunState: String, Sendable, Equatable {
    case stopped
    case starting
    case running
    case paused
    case error
    case pausing
    case resuming
    case stopping
    case saving
    case restoring

    /// Whether a VM in this state still runs, or is on its way up or
    /// down: a new Start is refused while it does.
    public var isActive: Bool {
        self != .stopped && self != .error
    }
}

/// The parts of a framework error that S06-06-devices-and-warm-start
/// ("Deciding permanence") asks wb-vmd to pass on.
public struct VMFailure: Error, Sendable, Equatable {
    /// The VZError.Code value, 0 for an error of wb-vmd's own.
    public var code: Int64
    public var failureReason: String
    /// The underlying POSIX errno, 0 when there is none.
    public var errno: Int32
    public var description: String

    public init(code: Int64 = 0, failureReason: String = "", errno: Int32 = 0, description: String)
    {
        self.code = code
        self.failureReason = failureReason
        self.errno = errno
        self.description = description
    }
}

/// The CPU and memory range the framework allows on this host
/// (S06-06-devices-and-warm-start, "Resources").
public struct ResourceLimits: Sendable, Equatable {
    public var cpuCount: ClosedRange<Int>
    public var memoryBytes: ClosedRange<UInt64>

    public init(cpuCount: ClosedRange<Int>, memoryBytes: ClosedRange<UInt64>) {
        self.cpuCount = cpuCount
        self.memoryBytes = memoryBytes
    }
}

/// What the engine builds: the checked description, the resources, and
/// the network device's MAC address and packet transport.
public struct BootRequest: Sendable, Equatable {
    public var description: VMDescription
    public var bundlePath: String
    public var cpuCount: Int
    public var memoryBytes: UInt64
    public var macAddress: MACAddress
    /// The VM end of the packet transport, for the description's one
    /// file-handle network device, or nil when it has none. The caller
    /// keeps it and closes it after makeVM: an engine that keeps the
    /// descriptor keeps its own copy (dup(2)).
    public var networkDescriptor: Int32?

    /// How many network devices the description has.
    public var networks: Int {
        description.devices.filter { device in
            if case .network = device {
                return true
            }
            return false
        }.count
    }
}

/// Called with each state change of a VM, and the failure that ended it
/// when the state is `.error`.
public typealias VMStateHandler = @MainActor (VMRunState, VMFailure?) -> Void

/// What an install reads from the restore image.
public struct RestoreImageInfo: Sendable, Equatable {
    /// The macOS build the image holds, for the log.
    public var buildVersion: String
    /// The data representation of the hardware model of the image's most
    /// featureful configuration that this host supports, or nil when this
    /// host can run none of them.
    public var hardwareModel: [UInt8]?

    public init(buildVersion: String, hardwareModel: [UInt8]?) {
        self.buildVersion = buildVersion
        self.hardwareModel = hardwareModel
    }
}

/// Called on the main actor while an install runs, with the fraction
/// done, from 0 to 1.
public typealias InstallProgressHandler = @MainActor (Double) -> Void

/// Builds VMs.
@MainActor
public protocol VMEngine: AnyObject {
    /// The resource range for this host.
    var limits: ResourceLimits { get }

    /// Builds a VM from a request whose description passed the
    /// device-set check. Throws when the framework refuses the
    /// configuration.
    func makeVM(_ request: BootRequest, onState: @escaping VMStateHandler) throws(VMFailure)
        -> any EngineVM

    /// Reads the restore image at `path`, the fixed one of the build slot.
    func restoreImage(at path: String) async throws(VMFailure) -> RestoreImageInfo

    /// The data representation of a new machine identifier.
    func newMachineIdentifier() -> [UInt8]

    /// Creates the auxiliary storage at `path` for the hardware model.
    /// Throws when a file is there already: it never overwrites one.
    func createAuxiliaryStorage(at path: String, hardwareModel: [UInt8]) throws(VMFailure)

    /// Installs macOS from the restore image at `restoreImagePath` into
    /// the VM a checked request describes, and calls `progress` while the
    /// install runs. The VM is stopped when the install returns.
    func install(
        _ request: BootRequest, restoreImagePath: String,
        progress: @escaping InstallProgressHandler
    ) async throws(VMFailure)
}

/// One VM the engine built.
@MainActor
public protocol EngineVM: AnyObject {
    func start() async throws(VMFailure)
    /// Asks the guest to shut down, and returns without waiting.
    func requestStop() throws(VMFailure)
    /// Stops the VM at once.
    func forceStop() async throws(VMFailure)
    /// Opens a connection to `port` in the guest, through the VM's one
    /// virtio socket device, and calls `completion` once, on the main
    /// actor, with the connection or the error.
    func connect(toPort port: UInt32, completion: @escaping GuestConnectHandler)
}
