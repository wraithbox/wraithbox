// `install` (I350), from the X18-vsock-handoff host tool: a new bundle
// with macOS installed from a restore image in `restore-images/`. The
// bundle has never booted, so `provision` can boot a clone of it.
import Foundation
import Virtualization
import WraithBoxVM

@MainActor
func installBundle(_ target: CachePath, image: CachePath, cache: SpikeCache, log: Log)
    async throws
{
    log.step("reading the restore image \(image.path)")
    let imageURL = URL(fileURLWithPath: image.path)
    let restore = try await VZMacOSRestoreImage.image(from: imageURL)
    guard let requirements = restore.mostFeaturefulSupportedConfiguration,
        requirements.hardwareModel.isSupported
    else {
        throw Failure("\(image.path): this host can't run macOS \(restore.buildVersion)")
    }
    try cache.makeParents(of: target)
    guard mkdir(target.path, 0o755) == 0 else {
        throw Failure.posix("mkdir", target.path)
    }
    let lock = try BundleLock.take(bundle: target.path)
    let size = machineSize(requirements)
    var record = BundleRecord(
        restoreImage: image.path, buildVersion: restore.buildVersion, cpuCount: size.cpuCount,
        memoryGiB: size.memoryGiB, installedAt: Date())
    log.step(
        "made \(target.path) for macOS \(restore.buildVersion), \(size.cpuCount) CPUs, \(size.memoryGiB) GiB"
    )
    try makeBundleFiles(target.path, model: requirements.hardwareModel)

    let network = try UnconnectedNetwork()
    let machine = VZVirtualMachine(
        configuration: try machineConfiguration(
            bundle: target.path, record: record, network: network.attachment,
            mac: .randomLocallyAdministered()))
    let installer = VZMacOSInstaller(virtualMachine: machine, restoringFromImageAt: imageURL)
    let stop = StopRequest { installer.progress.cancel() }
    let reporter = reportProgress(installer.progress, log: log)
    defer { reporter.cancel() }
    do {
        try await installer.install()
    } catch {
        let cause = stop.requested ? "it was stopped" : "it failed: \(error)"
        throw Failure(
            "\(target.path): the install is incomplete, \(cause). Remove it with mise run vm:clean")
    }
    // Stop the VM as soon as the install returns, as the X18 tool did.
    if machine.canStop {
        try? await machine.stop()
    }
    record.installedAt = Date()
    try record.write(bundle: target.path)
    log.step("installed \(target.path)")
    withExtendedLifetime(lock) {}
}

/// The hardware model, a new machine identifier, the auxiliary storage,
/// and the two disks as sparse files.
private func makeBundleFiles(_ bundle: String, model: VZMacHardwareModel) throws {
    try model.dataRepresentation.write(
        to: bundleURL(bundle, .hardwareModel), options: .withoutOverwriting)
    try VZMacMachineIdentifier().dataRepresentation.write(
        to: bundleURL(bundle, .machineIdentifier), options: .withoutOverwriting)
    _ = try VZMacAuxiliaryStorage(
        creatingStorageAt: bundleURL(bundle, .auxiliaryStorage), hardwareModel: model, options: [])
    try makeSparseFile(VMBundle(path: bundle).path(of: .systemDisk), gib: DiskSize.systemGiB)
    try makeSparseFile(VMBundle(path: bundle).path(of: .dataDisk), gib: DiskSize.dataGiB)
}

func makeSparseFile(_ path: String, gib: Int) throws(Failure) {
    let fd = open(path, O_RDWR | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600)
    guard fd >= 0 else {
        throw Failure.posix("create", path)
    }
    defer { close(fd) }
    guard ftruncate(fd, off_t(gib) << 30) == 0 else {
        throw Failure.posix("ftruncate", path)
    }
}

/// Logs the install's progress every ten percent.
@MainActor
private func reportProgress(_ progress: Progress, log: Log) -> Task<Void, Never> {
    Task { @MainActor in
        var reported = -1
        while !Task.isCancelled {
            let tens = Int(progress.fractionCompleted * 10) * 10
            if tens != reported {
                log.step("install \(tens)%")
                reported = tens
            }
            try? await Task.sleep(for: .seconds(5))
        }
    }
}
