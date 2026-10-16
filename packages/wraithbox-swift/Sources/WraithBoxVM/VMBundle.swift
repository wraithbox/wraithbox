// The files of a VM bundle, and the check that runs before anything opens
// one. A confined `wb-vmd` that opens a path its profile denies ends in an
// uncaught exception (X25-vmd-sandbox), so each file is checked with
// lstat and realpath first (S04-02-vm-provider, "The device set is fixed
// and checked").
import Darwin

/// A file at a fixed name in a VM bundle (S06-05-disks, "Bundle files").
/// Any other file in the bundle is ignored.
public enum BundleFile: String, Sendable, Equatable, CaseIterable {
    case hardwareModel = "hw.bin"
    case machineIdentifier = "mid.bin"
    case auxiliaryStorage = "aux.img"
    case systemDisk = "sys.img"
    case dataDisk = "data.wbdisk"
    case toolsDisk = "tools.img"
}

/// A VM bundle directory, at the path `wb-vmd` got from its slot.
public struct VMBundle: Sendable, Equatable {
    /// The bundle path, without a trailing slash.
    public let path: String

    public init(path: String) {
        var trimmed = path
        while trimmed.count > 1 && trimmed.hasSuffix("/") {
            trimmed.removeLast()
        }
        self.path = trimmed
    }

    /// The path of one of the bundle's files.
    public func path(of file: BundleFile) -> String {
        path + "/" + file.rawValue
    }

    /// Checks every bundle file: each must be present, a regular file with
    /// one link, and have its real path inside the bundle's real path.
    /// Returns the bundle's real path.
    ///
    /// The check is by path, because the disk image attachment opens its
    /// file by path. The attachment that takes a descriptor is for host
    /// block devices, a refused kind. So a file can change between this
    /// check and the open: the caller runs the check right before it
    /// builds the attachments, and the sandbox profile is the second
    /// layer (S04-02-vm-provider, "Profile").
    func checkFiles() throws(DeviceSetRefusal) -> String {
        guard let realBundle = realPath(path) else {
            throw DeviceSetRefusal(.bundleFileMissing, "bundle \(path): \(errnoText())")
        }
        for file in BundleFile.allCases {
            try checkFile(file, realBundle: realBundle)
        }
        return realBundle
    }

    private func checkFile(_ file: BundleFile, realBundle: String) throws(DeviceSetRefusal) {
        let filePath = path(of: file)
        var info = stat()
        guard lstat(filePath, &info) == 0 else {
            throw DeviceSetRefusal(.bundleFileMissing, "\(file.rawValue): \(errnoText())")
        }
        guard info.st_mode & S_IFMT == S_IFREG else {
            let kind = info.st_mode & S_IFMT == S_IFLNK ? "a symbolic link" : "not a regular file"
            throw DeviceSetRefusal(.fileOutsideBundle, "\(file.rawValue) is \(kind)")
        }
        // A hard link can name a file outside the bundle, and realpath
        // can't tell, so a bundle file has exactly one link.
        guard info.st_nlink == 1 else {
            throw DeviceSetRefusal(
                .fileOutsideBundle, "\(file.rawValue) has \(info.st_nlink) links")
        }
        let expected = realBundle + "/" + file.rawValue
        guard let real = realPath(filePath), real == expected else {
            throw DeviceSetRefusal(
                .fileOutsideBundle, "\(file.rawValue) resolves outside \(realBundle)")
        }
    }
}

/// realpath(3), or nil when it fails.
func realPath(_ path: String) -> String? {
    guard let resolved = realpath(path, nil) else {
        return nil
    }
    defer { free(resolved) }
    return String(cString: resolved)
}

private func errnoText() -> String {
    String(cString: strerror(errno))
}
