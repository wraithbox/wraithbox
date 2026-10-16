// The Virtualization framework side of wb-vmd. It maps a description
// that already passed the device-set check to Virtualization objects
// (S04-02-vm-provider, "The device set is fixed and checked"), and
// creates no device the description doesn't name. Boot and stop use
// APIs from macOS 12 to 14.
import Foundation
import Virtualization
import WraithBoxVM

/// The engine that runs real VMs.
@MainActor
public final class VirtualizationEngine: VMEngine {
    public init() {}

    public var limits: ResourceLimits {
        ResourceLimits(
            cpuCount: VZVirtualMachineConfiguration
                .minimumAllowedCPUCount...VZVirtualMachineConfiguration.maximumAllowedCPUCount,
            memoryBytes: VZVirtualMachineConfiguration
                .minimumAllowedMemorySize...VZVirtualMachineConfiguration.maximumAllowedMemorySize)
    }

    public func makeVM(_ request: BootRequest, onState: @escaping VMStateHandler) throws(VMFailure)
        -> any EngineVM
    {
        let configuration = try VirtualizationConfiguration.make(request)
        return VirtualizationVM(configuration: configuration, onState: onState)
    }
}

/// Builds a VZVirtualMachineConfiguration from a checked description.
@MainActor
enum VirtualizationConfiguration {
    /// The most bytes read from the hardware model and machine identifier
    /// files. Both are a few hundred bytes.
    static let maxIdentityBytes = 64 * 1024

    /// The display of the Mac graphics device, as X18-vsock-handoff and
    /// X25-vmd-sandbox measured it.
    static let display = (width: 1280, height: 800, pixelsPerInch: 80)

    static func make(_ request: BootRequest) throws(VMFailure) -> VZVirtualMachineConfiguration {
        let bundle = VMBundle(path: request.bundlePath)
        let configuration = VZVirtualMachineConfiguration()
        configuration.platform = try platform(bundle)
        configuration.bootLoader = VZMacOSBootLoader()
        configuration.cpuCount = request.cpuCount
        configuration.memorySize = request.memoryBytes
        for device in request.description.devices {
            try add(device, to: configuration)
        }
        do {
            try configuration.validate()
        } catch {
            throw VMFailure(error)
        }
        return configuration
    }

    private static func platform(_ bundle: VMBundle) throws(VMFailure) -> VZMacPlatformConfiguration
    {
        let platform = VZMacPlatformConfiguration()
        let model = try readIdentity(bundle.path(of: .hardwareModel))
        guard let hardwareModel = VZMacHardwareModel(dataRepresentation: model),
            hardwareModel.isSupported
        else {
            throw VMFailure(
                description:
                    "\(BundleFile.hardwareModel.rawValue) is not a supported hardware model")
        }
        platform.hardwareModel = hardwareModel
        let identifier = try readIdentity(bundle.path(of: .machineIdentifier))
        guard let machineIdentifier = VZMacMachineIdentifier(dataRepresentation: identifier) else {
            throw VMFailure(
                description: "\(BundleFile.machineIdentifier.rawValue) is not a machine identifier")
        }
        platform.machineIdentifier = machineIdentifier
        platform.auxiliaryStorage = VZMacAuxiliaryStorage(
            url: URL(fileURLWithPath: bundle.path(of: .auxiliaryStorage)))
        return platform
    }

    private static func readIdentity(_ path: String) throws(VMFailure) -> Data {
        guard let handle = FileHandle(forReadingAtPath: path) else {
            throw VMFailure(description: "can't open \(path)")
        }
        defer { try? handle.close() }
        let data: Data
        do {
            data = try handle.read(upToCount: maxIdentityBytes + 1) ?? Data()
        } catch {
            throw VMFailure(error)
        }
        guard data.count <= maxIdentityBytes else {
            throw VMFailure(description: "\(path) is over \(maxIdentityBytes) bytes")
        }
        return data
    }

    /// Adds one device. A kind outside the fixed set was refused by the
    /// check before this runs, and is refused here again.
    private static func add(_ device: VMDevice, to configuration: VZVirtualMachineConfiguration)
        throws(VMFailure)
    {
        switch device {
        case .vsock:
            configuration.socketDevices.append(VZVirtioSocketDeviceConfiguration())
        case .entropy:
            configuration.entropyDevices.append(VZVirtioEntropyDeviceConfiguration())
        case .disk(let disk):
            configuration.storageDevices.append(try blockDevice(disk))
        case .macGraphics(let displays):
            let graphics = VZMacGraphicsDeviceConfiguration()
            graphics.displays = (0..<displays).map { _ in
                VZMacGraphicsDisplayConfiguration(
                    widthInPixels: display.width, heightInPixels: display.height,
                    pixelsPerInch: display.pixelsPerInch)
            }
            configuration.graphicsDevices.append(graphics)
        case .usbKeyboard:
            configuration.keyboards.append(VZUSBKeyboardConfiguration())
        case .usbScreenPointer:
            configuration.pointingDevices.append(VZUSBScreenCoordinatePointingDeviceConfiguration())
        case .network, .directoryShare, .audioInput, .console, .storage, .unknown:
            throw VMFailure(description: "\(device.logName) is not in the fixed device set")
        }
    }

    /// A disk image on a virtio block device, with automatic caching and
    /// full synchronization (S06-05-disks, "Disks").
    private static func blockDevice(_ disk: VMDisk) throws(VMFailure)
        -> VZVirtioBlockDeviceConfiguration
    {
        do {
            let attachment = try VZDiskImageStorageDeviceAttachment(
                url: URL(fileURLWithPath: disk.path), readOnly: disk.readOnly,
                cachingMode: .automatic, synchronizationMode: .full)
            return VZVirtioBlockDeviceConfiguration(attachment: attachment)
        } catch {
            throw VMFailure(error)
        }
    }
}

/// One VZVirtualMachine, on the main queue.
@MainActor
final class VirtualizationVM: NSObject, EngineVM, VZVirtualMachineDelegate {
    private let machine: VZVirtualMachine
    private let onState: VMStateHandler
    private var observation: NSKeyValueObservation?
    private var lastFailure: VMFailure?

    init(configuration: VZVirtualMachineConfiguration, onState: @escaping VMStateHandler) {
        machine = VZVirtualMachine(configuration: configuration)
        self.onState = onState
        super.init()
        machine.delegate = self
        observation = machine.observe(\.state, options: [.new]) { [weak self] _, _ in
            MainActor.assumeIsolated {
                self?.stateChanged()
            }
        }
    }

    func start() async throws(VMFailure) {
        do {
            try await machine.start()
        } catch {
            throw VMFailure(error)
        }
    }

    func requestStop() throws(VMFailure) {
        do {
            try machine.requestStop()
        } catch {
            throw VMFailure(error)
        }
    }

    func forceStop() async throws(VMFailure) {
        do {
            try await machine.stop()
        } catch {
            throw VMFailure(error)
        }
    }

    nonisolated func virtualMachine(
        _ virtualMachine: VZVirtualMachine, didStopWithError error: any Error
    ) {
        let failure = VMFailure(error)
        MainActor.assumeIsolated {
            lastFailure = failure
            onState(.error, failure)
        }
    }

    nonisolated func guestDidStop(_ virtualMachine: VZVirtualMachine) {
        MainActor.assumeIsolated {
            stateChanged()
        }
    }

    private func stateChanged() {
        let state = VMRunState(machine.state)
        onState(state, state == .error ? lastFailure : nil)
    }
}

extension VMRunState {
    init(_ state: VZVirtualMachine.State) {
        switch state {
        case .stopped: self = .stopped
        case .running: self = .running
        case .paused: self = .paused
        case .error: self = .error
        case .starting: self = .starting
        case .pausing: self = .pausing
        case .resuming: self = .resuming
        case .stopping: self = .stopping
        case .saving: self = .saving
        case .restoring: self = .restoring
        @unknown default: self = .error
        }
    }
}

extension VMFailure {
    /// The four parts of a framework error (S06-06-devices-and-warm-start,
    /// "Deciding permanence").
    init(_ error: any Error) {
        let nsError = error as NSError
        var posix: Int32 = 0
        if let underlying = nsError.userInfo[NSUnderlyingErrorKey] as? NSError,
            underlying.domain == NSPOSIXErrorDomain
        {
            posix = Int32(clamping: underlying.code)
        }
        self.init(
            code: nsError.domain == VZErrorDomain ? Int64(nsError.code) : 0,
            failureReason: nsError.localizedFailureReason ?? "", errno: posix,
            description: nsError.localizedDescription)
    }
}
