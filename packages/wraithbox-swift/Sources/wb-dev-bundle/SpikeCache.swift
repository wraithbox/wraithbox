// The path checks of every command (I350): the tool works only in the
// spike cache, `~/Library/Caches/wraithbox-spikes/`, which also holds the
// spikes' bundles (docs/agents/spikes.md, "VM bundles"), and it
// refuses what `mise run spike:clean` refuses. The home volume's APFS
// ignores case and Unicode normalization, so every existing component
// must be spelled byte for byte as its directory entry, and the checks
// apply to those names. The checks are by path, and only the maintainer
// and agents on the host write in the cache. So a change between a check
// and its use comes from them, not from a guest. `clean` removes with
// openat and unlinkat, which follow no symbolic link.
import Foundation

/// A path in the cache that passed the checks for its use.
struct CachePath: Sendable, Equatable {
    /// The absolute path.
    let path: String
    /// The components below the cache directory.
    let components: [String]
}

/// What a command does with a path, which decides the checks.
enum PathUse: Sendable {
    /// A new bundle: the path is free, and no directory above it holds a
    /// `KEEP` file.
    case create
    /// A bundle the command changes or removes: it exists, and neither it
    /// nor a directory above it holds a `KEEP` file.
    case change
    /// A bundle the command only reads or signals: it exists.
    case read
}

/// The spike cache, `<home>/Library/Caches/wraithbox-spikes`.
struct SpikeCache: Sendable {
    static let restoreImages = "restore-images"
    static let keepFile = "KEEP"

    /// The cache directory, without a trailing slash.
    let root: String

    /// The cache of the user this process runs as. The home directory
    /// comes from the account database, not from `$HOME`, so no
    /// environment variable can point the tool elsewhere.
    static func forCurrentUser() throws(Refusal) -> SpikeCache {
        guard let entry = getpwuid(getuid()), let home = entry.pointee.pw_dir else {
            throw Refusal(
                .cacheRoot, "uid \(getuid())", "the account database has no home directory")
        }
        let path = String(cString: home)
        guard path.hasPrefix("/") else {
            throw Refusal(.cacheRoot, path, "the home directory is not an absolute path")
        }
        return SpikeCache(root: path + "/Library/Caches/wraithbox-spikes")
    }

    /// Checks `argument` for `use` and returns its path. A relative path is
    /// taken relative to the cache, never to the working directory.
    func bundle(_ argument: String, for use: PathUse) throws(Refusal) -> CachePath {
        try checkRoot()
        let target = try place(argument)
        guard !Self.isRestoreImages(target.components[0]) else {
            throw Refusal(
                .restoreImages, target.path, "\(Self.restoreImages)/ holds only restore images")
        }
        let kind = try walk(target)
        switch use {
        case .create:
            guard kind == nil else {
                throw Refusal(.targetExists, target.path, "it exists already")
            }
            try checkKeep(target)
        case .change:
            try requireDirectory(target, kind)
            try checkKeep(target)
        case .read:
            try requireDirectory(target, kind)
        }
        return target
    }

    /// Checks a restore image argument: a regular file below
    /// `restore-images/`.
    func restoreImage(_ argument: String) throws(Refusal) -> CachePath {
        try checkRoot()
        let image = try place(argument)
        guard image.components.count > 1, Self.isRestoreImages(image.components[0]) else {
            throw Refusal(
                .restoreImagePath, image.path, "a restore image is in \(Self.restoreImages)/")
        }
        switch try walk(image) {
        case nil:
            throw Refusal(.missing, image.path, "there is no such file")
        case S_IFREG:
            return image
        default:
            throw Refusal(.restoreImagePath, image.path, "it is not a regular file")
        }
    }

    /// Makes the missing directories above a new bundle, each a real
    /// directory in the cache.
    func makeParents(of target: CachePath) throws(Failure) {
        var path = root
        for component in target.components.dropLast() {
            path += "/" + component
            if mkdir(path, 0o755) != 0 && errno != EEXIST {
                throw Failure.posix("mkdir", path)
            }
        }
    }

    /// Whether a name is `restore-images` in any case or normalization,
    /// as the file system compares names.
    static func isRestoreImages(_ name: String) -> Bool {
        name.compare(restoreImages, options: [.caseInsensitive]) == .orderedSame
    }

    /// The cache is there, and a real directory.
    private func checkRoot() throws(Refusal) {
        var info = stat()
        guard lstat(root, &info) == 0 else {
            throw Refusal(.cacheRoot, root, "the cache is missing: \(errnoText())")
        }
        guard info.st_mode & S_IFMT == S_IFDIR else {
            let what = info.st_mode & S_IFMT == S_IFLNK ? "a symbolic link" : "not a directory"
            throw Refusal(.cacheRoot, root, "the cache directory is \(what)")
        }
    }

    /// The absolute path and components of an argument, checked by text
    /// alone.
    private func place(_ argument: String) throws(Refusal) -> CachePath {
        var relative = Substring(argument)
        if argument.hasPrefix("/") {
            guard argument.hasPrefix(root + "/") else {
                throw Refusal(.outsideCache, argument, "it is not inside \(root)/")
            }
            relative = argument.dropFirst(root.count + 1)
        }
        while relative.hasSuffix("/") {
            relative = relative.dropLast()
        }
        let components = relative.split(separator: "/", omittingEmptySubsequences: false)
            .map(String.init)
        guard !relative.isEmpty, components.allSatisfy({ !["", ".", ".."].contains($0) }) else {
            throw Refusal(
                .pathComponent, argument,
                "it names no path below the cache, or has an empty, . or .. component")
        }
        return CachePath(
            path: root + "/" + components.joined(separator: "/"), components: components)
    }

    /// Walks the path from the cache down with lstat. Refuses a symbolic
    /// link at any level, a component above the last that isn't a
    /// directory, and an existing component that isn't spelled byte for
    /// byte as its directory entry. Returns the file type of the last component, or nil when
    /// the path ends in components that don't exist.
    private func walk(_ target: CachePath) throws(Refusal) -> mode_t? {
        var path = root
        for (index, component) in target.components.enumerated() {
            let parent = path
            path += "/" + component
            var info = stat()
            guard lstat(path, &info) == 0 else {
                guard errno == ENOENT else {
                    throw Refusal(.missing, path, "lstat failed: \(errnoText())")
                }
                return nil
            }
            guard directory(parent, hasEntry: component) else {
                throw Refusal(
                    .nameSpelling, path,
                    "no entry in \(parent) is spelled byte for byte like it; name it as ls shows it"
                )
            }
            let type = info.st_mode & S_IFMT
            guard type != S_IFLNK else {
                throw Refusal(.symbolicLink, path, "it is a symbolic link")
            }
            if index == target.components.count - 1 {
                return type
            }
            guard type == S_IFDIR else {
                throw Refusal(.notDirectory, path, "it is not a directory")
            }
        }
        return nil
    }

    private func requireDirectory(_ target: CachePath, _ kind: mode_t?) throws(Refusal) {
        guard let kind else {
            throw Refusal(.missing, target.path, "there is no such bundle")
        }
        guard kind == S_IFDIR else {
            throw Refusal(.notDirectory, target.path, "it is not a directory")
        }
    }

    /// Refuses when the path, or a directory between it and the cache,
    /// holds a `KEEP` file. The first line of the file names the issue
    /// that still needs the bundles (docs/agents/spikes.md, "VM bundles").
    private func checkKeep(_ target: CachePath) throws(Refusal) {
        var path = root
        for component in target.components {
            path += "/" + component
            if let reason = keepReason(in: path) {
                throw Refusal(.keepFile, target.path, "\(path)/\(Self.keepFile) says: \(reason)")
            }
        }
    }
}

/// The first line of a `KEEP` file in `directory`, or nil when it has
/// none.
func keepReason(in directory: String) -> String? {
    let path = directory + "/" + SpikeCache.keepFile
    var info = stat()
    guard lstat(path, &info) == 0 else {
        return nil
    }
    guard info.st_mode & S_IFMT == S_IFREG else {
        return "(\(SpikeCache.keepFile) is not a regular file)"
    }
    let text = (try? readSmallFile(path, limit: 200)) ?? ""
    let line = text.split(separator: "\n", omittingEmptySubsequences: false).first ?? ""
    let trimmed = line.trimmingCharacters(in: .whitespaces)
    return trimmed.isEmpty ? "(no reason given)" : trimmed
}

/// Whether `directory` has an entry whose name is byte for byte `name`.
/// lstat finds a name in any case or normalization, readdir gives the
/// spelling on disk.
func directory(_ directory: String, hasEntry name: String) -> Bool {
    guard let stream = opendir(directory) else {
        return false
    }
    defer { closedir(stream) }
    let wanted = Array(name.utf8)
    while let entry = readdir(stream) {
        let found = withUnsafeBytes(of: entry.pointee.d_name) { raw in
            Array(raw.prefix(Int(entry.pointee.d_namlen)))
        }
        if found == wanted {
            return true
        }
    }
    return false
}

/// Up to `limit` bytes of a regular file, as UTF-8 with invalid bytes
/// replaced. Refuses to follow a symbolic link.
func readSmallFile(_ path: String, limit: Int) throws(Failure) -> String {
    let fd = open(path, O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK)
    guard fd >= 0 else {
        throw Failure.posix("open", path)
    }
    defer { close(fd) }
    var buffer = [UInt8](repeating: 0, count: limit)
    let count = buffer.withUnsafeMutableBytes { read(fd, $0.baseAddress, limit) }
    guard count >= 0 else {
        throw Failure.posix("read", path)
    }
    return String(decoding: buffer.prefix(count), as: UTF8.self)
}

func errnoText() -> String {
    String(cString: strerror(errno))
}
