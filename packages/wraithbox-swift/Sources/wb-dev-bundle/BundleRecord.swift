// What the tool knows about a bundle it made, in `dev-bundle.json` next to
// the bundle files. `wb-vmd` ignores any file in a bundle that isn't a
// bundle file (S06-05-disks, "Bundle files").
import Foundation

struct BundleRecord: Codable, Equatable, Sendable {
    static let fileName = "dev-bundle.json"

    /// The restore image's path, and the macOS build it installed.
    var restoreImage: String
    var buildVersion: String
    var cpuCount: Int
    var memoryGiB: Int
    var installedAt: Date
    /// When `provision` started the VM. Set before the first boot, so a
    /// bundle whose boot failed half-way counts as booted.
    var bootedAt: Date?
    /// The SSH user `provision` asked the guest to create.
    var sshUser: String?

    /// The record of the bundle at `bundle`. Refuses a bundle without one,
    /// which `install` didn't finish or didn't make.
    static func read(bundle: String) throws -> BundleRecord {
        let path = bundle + "/" + fileName
        var info = stat()
        guard lstat(path, &info) == 0, info.st_mode & S_IFMT == S_IFREG else {
            throw Refusal(
                .notInstalled, bundle,
                "it has no \(fileName), so mise run vm:install didn't make it or didn't finish")
        }
        let text = try readSmallFile(path, limit: 64 << 10)
        return try decoder.decode(BundleRecord.self, from: Data(text.utf8))
    }

    /// Writes the record into the bundle at `bundle`, replacing the old
    /// one.
    func write(bundle: String) throws {
        let data = try encoder.encode(self)
        let path = bundle + "/" + Self.fileName
        let fd = open(path, O_WRONLY | O_CREAT | O_TRUNC | O_NOFOLLOW | O_CLOEXEC, 0o644)
        guard fd >= 0 else {
            throw Failure.posix("open", path)
        }
        defer { close(fd) }
        let written = data.withUnsafeBytes { Darwin.write(fd, $0.baseAddress, $0.count) }
        guard written == data.count else {
            throw Failure.posix("write", path)
        }
    }
}

private var encoder: JSONEncoder {
    let encoder = JSONEncoder()
    encoder.dateEncodingStrategy = .iso8601
    encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
    return encoder
}

private var decoder: JSONDecoder {
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601
    return decoder
}
