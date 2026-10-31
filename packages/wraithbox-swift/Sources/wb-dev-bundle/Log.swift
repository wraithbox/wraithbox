// The tool's output: one line per step, and one line per refusal that
// names the rule behind it (I350, docs/agents/host-work.md, "VM bundles").
import Foundation

/// Where the tool writes its lines.
struct Log: Sendable {
    let write: @Sendable (String) -> Void

    /// A step the tool takes, or what it found.
    func step(_ text: String) {
        write("wb-dev-bundle: \(text)")
    }

    func refused(_ refusal: Refusal) {
        write("wb-dev-bundle: \(refusal)")
    }

    static let standardError = Log { line in
        FileHandle.standardError.write(Data((line + "\n").utf8))
    }
}

/// The rules the tool refuses by. Each refusal names one.
enum Rule: String, Sendable, CaseIterable {
    /// The cache is a real directory, from the account database.
    case cacheRoot = "cache-root"
    /// Paths are inside `~/Library/Caches/wraithbox-spikes/`.
    case outsideCache = "outside-cache"
    /// No empty, `.` or `..` component in a path.
    case pathComponent = "path-component"
    /// No symbolic link anywhere below the cache in a path.
    case symbolicLink = "symbolic-link"
    /// An existing component is spelled byte for byte as its directory
    /// entry, because APFS ignores case and Unicode normalization.
    case nameSpelling = "name-spelling"
    /// Every component but the last is a directory, and a bundle is one.
    case notDirectory = "not-a-directory"
    /// `restore-images/` holds only restore images, and no bundle.
    case restoreImages = "restore-images-kept"
    /// A directory with a `KEEP` file, or under one, is not changed.
    case keepFile = "keep-file"
    /// A new bundle's path is free.
    case targetExists = "target-exists"
    /// The bundle or image is there.
    case missing = "missing"
    /// A bundle whose VM runs is not cloned, provisioned or removed.
    case vmRunning = "vm-running"
    /// Only a bundle `vm:install` finished is provisioned.
    case notInstalled = "not-installed"
    /// Provisioning works only on a bundle that never booted.
    case alreadyBooted = "already-booted"
    /// A restore image is a regular file in `restore-images/`.
    case restoreImagePath = "restore-image-path"
    /// A removal stays on the cache's device.
    case otherDevice = "other-device"
    /// The bundle files a VM opens are regular files, not links.
    case bundleFile = "bundle-file"
    /// Guest provisioning needs macOS 27 on the host (HostRequirement).
    case hostUnsupported = "host-unsupported"
    /// The command line names a command and its arguments.
    case usage = "usage"
}

/// Why the tool won't do what it was asked, and the rule.
struct Refusal: Error, CustomStringConvertible, Equatable {
    let rule: Rule
    let subject: String
    let reason: String

    init(_ rule: Rule, _ subject: String, _ reason: String) {
        self.rule = rule
        self.subject = subject
        self.reason = reason
    }

    var description: String {
        "\(subject): refused, \(reason) (rule: \(rule.rawValue))"
    }
}

/// A failure that isn't a refusal: a system call or the Virtualization
/// framework failed.
struct Failure: Error, CustomStringConvertible {
    let description: String

    init(_ description: String) {
        self.description = description
    }

    /// A failed system call, with errno's text.
    static func posix(_ what: String, _ path: String) -> Failure {
        Failure("\(what) \(path): \(String(cString: strerror(errno)))")
    }
}
