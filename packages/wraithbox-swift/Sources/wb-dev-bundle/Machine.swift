// The VM configuration of a test bundle, from the X18-vsock-handoff host
// tool: a macOS guest with the bundle's system and data disks, one
// display, a keyboard and a pointer. The caller picks the network
// attachment. The files have the names `wb-vmd` opens (S06-05-disks,
// "Bundle files"), so a clone goes to a slot's bundle path without a
// rename.
import Foundation
import Virtualization
import WraithBoxVM

/// Sizes of the disks `install` makes, as sparse files. 8 GiB for the
/// data disk is what the needs-vm tests add to a spike bundle without one.
enum DiskSize {
    static let systemGiB = 64
    static let dataGiB = 8
}

/// The CPU count and memory of a test guest: four CPUs and 4 GiB, or the
/// restore image's minimum when it is more, within the host's limits.
/// Fails when the host's maximum is below the image's minimum.
func machineSize(_ requirements: VZMacOSConfigurationRequirements) throws(Failure) -> (
    cpuCount: Int, memoryGiB: Int
) {
    let minimumCPUs = requirements.minimumSupportedCPUCount
    let maximumCPUs = VZVirtualMachineConfiguration.maximumAllowedCPUCount
    let minimumGiB = Int((requirements.minimumSupportedMemorySize + (1 << 30) - 1) >> 30)
    let maximumGiB = Int(VZVirtualMachineConfiguration.maximumAllowedMemorySize >> 30)
    guard maximumCPUs >= minimumCPUs, maximumGiB >= minimumGiB else {
        throw Failure(
            "the image needs \(minimumCPUs) CPUs and \(minimumGiB) GiB, and this host allows"
                + " \(maximumCPUs) CPUs and \(maximumGiB) GiB")
    }
    return (min(max(4, minimumCPUs), maximumCPUs), min(max(4, minimumGiB), maximumGiB))
}

func bundleURL(_ bundle: String, _ file: BundleFile) -> URL {
    URL(fileURLWithPath: VMBundle(path: bundle).path(of: file))
}

/// The files a test VM opens: the tools disk isn't one of them.
let machineFiles: [BundleFile] = [
    .hardwareModel, .machineIdentifier, .auxiliaryStorage, .systemDisk, .dataDisk,
]

/// Refuses unless each file the VM opens is a regular file, so no
/// symbolic link in the bundle points the VM at another file.
func checkMachineFiles(_ bundle: String) throws(Refusal) {
    for file in machineFiles {
        let path = VMBundle(path: bundle).path(of: file)
        var info = stat()
        guard lstat(path, &info) == 0, info.st_mode & S_IFMT == S_IFREG else {
            throw Refusal(.bundleFile, path, "it is missing, or not a regular file")
        }
    }
}

/// The configuration of the bundle's VM with `network` as its one network
/// device.
@MainActor
func machineConfiguration(
    bundle: String, record: BundleRecord, network: VZNetworkDeviceAttachment,
    mac: VZMACAddress
) throws -> VZVirtualMachineConfiguration {
    try checkMachineFiles(bundle)
    let platform = VZMacPlatformConfiguration()
    let hardware = try Data(contentsOf: bundleURL(bundle, .hardwareModel))
    let identifier = try Data(contentsOf: bundleURL(bundle, .machineIdentifier))
    guard let model = VZMacHardwareModel(dataRepresentation: hardware),
        let machine = VZMacMachineIdentifier(dataRepresentation: identifier)
    else {
        throw Failure("\(bundle): hw.bin or mid.bin doesn't parse")
    }
    platform.hardwareModel = model
    platform.machineIdentifier = machine
    platform.auxiliaryStorage = VZMacAuxiliaryStorage(url: bundleURL(bundle, .auxiliaryStorage))

    let configuration = VZVirtualMachineConfiguration()
    configuration.platform = platform
    configuration.bootLoader = VZMacOSBootLoader()
    configuration.cpuCount = record.cpuCount
    configuration.memorySize = UInt64(record.memoryGiB) << 30
    let graphics = VZMacGraphicsDeviceConfiguration()
    graphics.displays = [
        VZMacGraphicsDisplayConfiguration(
            widthInPixels: 1280, heightInPixels: 800, pixelsPerInch: 80)
    ]
    configuration.graphicsDevices = [graphics]
    configuration.storageDevices = try [BundleFile.systemDisk, .dataDisk].map { file in
        let attachment = try VZDiskImageStorageDeviceAttachment(
            url: bundleURL(bundle, file), readOnly: false, cachingMode: .automatic,
            synchronizationMode: .full)
        return VZVirtioBlockDeviceConfiguration(attachment: attachment)
    }
    let device = VZVirtioNetworkDeviceConfiguration()
    device.attachment = network
    device.macAddress = mac
    configuration.networkDevices = [device]
    configuration.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
    configuration.keyboards = [VZUSBKeyboardConfiguration()]
    configuration.pointingDevices = [VZUSBScreenCoordinatePointingDeviceConfiguration()]
    try configuration.validate()
    return configuration
}

/// A network device with nothing behind it: the guest end of a datagram
/// socket pair whose host end nobody reads, as in the X17-image-build
/// install. The installer needs no network.
final class UnconnectedNetwork {
    let attachment: VZFileHandleNetworkDeviceAttachment
    private let hostEnd: Int32

    init() throws {
        var fds: [Int32] = [0, 0]
        guard socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds) == 0 else {
            throw Failure.posix("socketpair", "")
        }
        hostEnd = fds[0]
        attachment = VZFileHandleNetworkDeviceAttachment(
            fileHandle: FileHandle(fileDescriptor: fds[1], closeOnDealloc: true))
    }

    deinit {
        close(hostEnd)
    }
}

/// Polls until the VM has stopped or failed, `stop` was requested, or
/// `deadline` passed. Returns what ended the wait.
@MainActor
func waitForEnd(
    _ machine: VZVirtualMachine, stop: StopRequest, deadline: ContinuousClock.Instant
) async -> String {
    while ContinuousClock.now < deadline {
        if machine.state == .stopped || machine.state == .error {
            return "the guest stopped"
        }
        if stop.requested {
            return "a stop was requested"
        }
        try? await Task.sleep(for: .milliseconds(500))
    }
    return "the time limit passed"
}

/// Asks the guest to shut down, and stops the VM when it hasn't after
/// `StopRequest.guestGrace`.
@MainActor
func shutDown(_ machine: VZVirtualMachine, log: Log) async {
    guard machine.state != .stopped && machine.state != .error else {
        return
    }
    if machine.canRequestStop {
        log.step("asking the guest to shut down")
        try? machine.requestStop()
        let deadline = ContinuousClock.now + StopRequest.guestGrace
        while ContinuousClock.now < deadline && machine.state != .stopped {
            try? await Task.sleep(for: .milliseconds(500))
        }
    }
    if machine.state != .stopped && machine.canStop {
        log.step("stopping the VM")
        try? await machine.stop()
    }
    log.step("VM state: \(machine.state.rawValue)")
}
