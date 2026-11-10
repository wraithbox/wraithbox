// The build slot's install (S04-10-vmd-install, "`wb-vmd-install`", "The
// build bundle"): macOS from the fixed restore image into the fixed build
// bundle. The request names no path and no image. The install creates
// the identity files and the system disk in the bundle, never over a file
// that is there, so it can't change a bundle that holds an install
// already. wb-hostd puts the guest tools disk and the empty data disk in
// the bundle before it starts the build.
import Darwin
import WraithBoxVM

/// The steps of one install in the build bundle. VMSupervisor runs them,
/// after its own checks, at most once per instance.
@MainActor
final class BuildInstaller {
    /// The system disk's size: 64 GiB, as X17-image-build built it.
    nonisolated static let systemDiskBytes: off_t = 64 << 30

    /// The files an install creates. A bundle that holds any of them is
    /// refused before any file is touched.
    nonisolated static let createdFiles: [BundleFile] = [
        .hardwareModel, .machineIdentifier, .auxiliaryStorage, .systemDisk,
    ]

    private let engine: any VMEngine
    private let bundle: VMBundle
    private let restoreImagePath: String
    private let log: EventLog
    /// Set when an install starts, and never cleared: a second install
    /// in the same instance is refused, whether the first one failed or
    /// not.
    private(set) var attempted = false

    init(engine: any VMEngine, bundlePath: String, restoreImagePath: String, log: EventLog) {
        self.engine = engine
        bundle = VMBundle(path: bundlePath)
        self.restoreImagePath = restoreImagePath
        self.log = log
    }

    /// Checks the bundle, reads the restore image and creates the files
    /// the install writes. Throws `vmd-install-refused` before it touches
    /// a file, `vmd-restore-image-unsupported` for an image this host
    /// can't run, and `vm-install-failed` when a step fails.
    func prepare() async throws(VMRefusal) {
        guard !attempted else {
            throw VMRefusal(.installRefused, .failedPrecondition, "this instance installed already")
        }
        try checkEmpty()
        attempted = true
        let image: RestoreImageInfo
        do {
            image = try await engine.restoreImage(at: restoreImagePath)
        } catch {
            throw VMRefusal(.installFailed, .failedPrecondition, error.description, failure: error)
        }
        guard let model = image.hardwareModel else {
            throw VMRefusal(
                .restoreImageUnsupported, .failedPrecondition,
                "this host can't run macOS \(image.buildVersion)")
        }
        log.info("install-restore-image", ["build_version": .string(image.buildVersion)])
        do {
            try Self.create(bundle.path(of: .hardwareModel), bytes: model)
            try Self.create(
                bundle.path(of: .machineIdentifier), bytes: engine.newMachineIdentifier())
            try engine.createAuxiliaryStorage(
                at: bundle.path(of: .auxiliaryStorage), hardwareModel: model)
            try Self.createSparse(bundle.path(of: .systemDisk), size: Self.systemDiskBytes)
        } catch {
            throw VMRefusal(.installFailed, .failedPrecondition, error.description, failure: error)
        }
        log.info("install-files-created", ["files": .strings(Self.createdFiles.map(\.rawValue))])
    }

    /// Runs the installer on a checked request.
    func install(_ request: BootRequest, progress: @escaping InstallProgressHandler)
        async throws(VMRefusal)
    {
        do {
            try await engine.install(
                request, restoreImagePath: restoreImagePath, progress: progress)
        } catch {
            throw VMRefusal(.installFailed, .failedPrecondition, error.description, failure: error)
        }
    }

    /// Refuses a bundle that holds a file the install creates, as any
    /// kind of file or a symbolic link.
    private func checkEmpty() throws(VMRefusal) {
        for file in Self.createdFiles {
            var info = stat()
            if lstat(bundle.path(of: file), &info) == 0 {
                throw VMRefusal(
                    .installRefused, .failedPrecondition, "the bundle holds \(file.rawValue)")
            }
            guard errno == ENOENT else {
                throw VMRefusal(
                    .installRefused, .failedPrecondition,
                    "lstat \(file.rawValue): \(String(cString: strerror(errno)))")
            }
        }
    }

    /// Creates `path` with `bytes`. It never opens an existing file or
    /// follows a symbolic link.
    static func create(_ path: String, bytes: [UInt8]) throws(VMFailure) {
        let fd = try createExclusive(path)
        defer { close(fd) }
        var written = 0
        while written < bytes.count {
            let count = bytes[written...].withUnsafeBytes { write(fd, $0.baseAddress, $0.count) }
            guard count > 0 else {
                throw posixFailure("write", path)
            }
            written += count
        }
    }

    /// Creates a sparse file of `size` bytes at `path`, never over an
    /// existing file.
    static func createSparse(_ path: String, size: off_t) throws(VMFailure) {
        let fd = try createExclusive(path)
        defer { close(fd) }
        guard ftruncate(fd, size) == 0 else {
            throw posixFailure("ftruncate", path)
        }
    }

    private static func createExclusive(_ path: String) throws(VMFailure) -> Int32 {
        let fd = open(path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else {
            throw posixFailure("create", path)
        }
        return fd
    }

    private static func posixFailure(_ call: String, _ path: String) -> VMFailure {
        let error = errno
        return VMFailure(
            errno: error, description: "\(call) \(path): \(String(cString: strerror(error)))")
    }
}

/// The whole percents an install reports: rising, at most one each, and
/// 100 only once the install succeeded, which the caller reports.
struct InstallPercent {
    private(set) var last: UInt32?

    /// The percent to report for `fraction`, or nil when it isn't above
    /// the last one. A fraction at or above 1 reports 99.
    mutating func next(_ fraction: Double) -> UInt32? {
        guard fraction.isFinite else {
            return nil
        }
        // Clamped before the conversion, which traps on a value that
        // doesn't fit.
        let percent = min(UInt32(min(max(fraction, 0), 1) * 100), 99)
        if let last, percent <= last {
            return nil
        }
        last = percent
        return percent
    }
}
