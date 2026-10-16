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

/// What the engine builds: the checked description and the resources.
public struct BootRequest: Sendable, Equatable {
    public var description: VMDescription
    public var bundlePath: String
    public var cpuCount: Int
    public var memoryBytes: UInt64
}

/// Called with each state change of a VM, and the failure that ended it
/// when the state is `.error`.
public typealias VMStateHandler = @MainActor (VMRunState, VMFailure?) -> Void

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
}

/// One VM the engine built.
@MainActor
public protocol EngineVM: AnyObject {
    func start() async throws(VMFailure)
    /// Asks the guest to shut down, and returns without waiting.
    func requestStop() throws(VMFailure)
    /// Stops the VM at once.
    func forceStop() async throws(VMFailure)
}
