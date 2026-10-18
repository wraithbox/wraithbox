// `clone` and `clean` (I350): an APFS clone of a bundle, and the removal of
// a directory in the cache. Neither touches a bundle whose VM runs, and
// each holds the bundle's lock while it works.
import Foundation

/// Makes `target` an APFS clone of `source` with clonefile(2), which
/// copies symbolic links in the bundle as links. Refuses while the
/// source's VM runs, because its disks change under the clone. The clone
/// gets no lock file, so it names no process.
func cloneBundle(
    from source: CachePath, to target: CachePath, cache: SpikeCache, log: Log
) throws {
    if let pid = BundleLock.holder(bundle: source.path) {
        throw Refusal(.vmRunning, source.path, "process \(pid) runs its VM")
    }
    let lock = try BundleLock.take(bundle: source.path)
    try cache.makeParents(of: target)
    log.step("cloning \(source.path) to \(target.path)")
    guard clonefile(source.path, target.path, UInt32(CLONE_NOFOLLOW)) == 0 else {
        throw Failure.posix("clonefile to", target.path)
    }
    let copiedLock = target.path + "/" + BundleLock.fileName
    guard unlink(copiedLock) == 0 || errno == ENOENT else {
        throw Failure.posix("unlink", copiedLock)
    }
    log.step("cloned \(target.path)")
    withExtendedLifetime(lock) {}
}

/// How a removal reads a file's metadata: lstat(2), or a stand-in in
/// tests.
typealias Lstat = @Sendable (String) -> stat?

let realLstat: Lstat = { path in
    var info = stat()
    return lstat(path, &info) == 0 ? info : nil
}

/// Removes `target` and everything in it. Before it removes anything, it
/// walks the tree and refuses when an entry is on another device than the
/// cache, such as a disk image's volume mounted in a bundle, when a
/// directory in it holds a `KEEP` file, or when a VM runs from a bundle in
/// it. It walks again under the target's lock, then removes with openat
/// and unlinkat from the cache down, following no symbolic link.
func cleanBundle(
    _ target: CachePath, cache: SpikeCache, log: Log, lstat: @escaping Lstat = realLstat
)
    throws
{
    guard let rootInfo = lstat(cache.root) else {
        throw Failure.posix("lstat", cache.root)
    }
    let walk = RemovalWalk(device: rootInfo.st_dev, lstat: lstat)
    _ = try walk.check(target.path, ownLock: false)
    let lock = try BundleLock.take(bundle: target.path)
    let bytes = try walk.check(target.path, ownLock: true)
    log.step("removing \(target.path)")
    try removeTree(target, cache: cache, device: rootInfo.st_dev)
    log.step(
        "removed \(target.path), \(bytes >> 20) MiB of allocated blocks"
            + " (APFS clones can share some of them)")
    withExtendedLifetime(lock) {}
}

/// The check before a removal.
private struct RemovalWalk {
    let device: dev_t
    let lstat: Lstat

    /// The allocated bytes under `path`, or a refusal. With `ownLock`,
    /// the lock of `path` itself is this process's and isn't a running VM.
    func check(_ path: String, ownLock: Bool) throws -> Int64 {
        guard let info = lstat(path) else {
            throw Failure.posix("lstat", path)
        }
        guard info.st_dev == device else {
            throw Refusal(
                .otherDevice, path,
                "it is on another device than the cache; detach mounted volumes first")
        }
        var bytes = Int64(info.st_blocks) * 512
        guard info.st_mode & S_IFMT == S_IFDIR else {
            return bytes
        }
        if let reason = keepReason(in: path) {
            throw Refusal(.keepFile, path, "\(path)/\(SpikeCache.keepFile) says: \(reason)")
        }
        if !ownLock, let pid = BundleLock.holder(bundle: path) {
            throw Refusal(.vmRunning, path, "process \(pid) runs its VM")
        }
        for name in try FileManager.default.contentsOfDirectory(atPath: path) {
            bytes += try check(path + "/" + name, ownLock: false)
        }
        return bytes
    }
}

/// Removes the target with descriptors: each directory from the cache down
/// is opened with O_NOFOLLOW, and every entry is removed with unlinkat
/// relative to its directory. A symbolic link swapped in after the check
/// is removed or refused, never followed. An entry on another device stops
/// the removal.
private func removeTree(_ target: CachePath, cache: SpikeCache, device: dev_t) throws {
    var parent = open(cache.root, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
    guard parent >= 0 else {
        throw Failure.posix("open", cache.root)
    }
    defer { close(parent) }
    for component in target.components.dropLast() {
        let next = openat(parent, component, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
        guard next >= 0 else {
            throw Failure.posix("open", component)
        }
        close(parent)
        parent = next
    }
    try removeEntry(cName(target.components.last ?? ""), in: parent, device: device)
}

/// A name as the NUL-terminated bytes the system calls take.
private func cName(_ name: String) -> [CChar] {
    Array(name.utf8CString)
}

/// A name for the log, with bytes that aren't UTF-8 replaced.
private func display(_ name: [CChar]) -> String {
    String(decoding: name.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self)
}

/// Removes one entry of an open directory, by its bytes on disk, so a name
/// that isn't valid UTF-8 is removed too.
private func removeEntry(_ name: [CChar], in parent: Int32, device: dev_t) throws {
    var info = stat()
    guard fstatat(parent, name, &info, AT_SYMLINK_NOFOLLOW) == 0 else {
        throw Failure.posix("fstatat", display(name))
    }
    guard info.st_dev == device else {
        throw Refusal(.otherDevice, display(name), "it moved to another device during the removal")
    }
    guard info.st_mode & S_IFMT == S_IFDIR else {
        guard unlinkat(parent, name, 0) == 0 else {
            throw Failure.posix("unlinkat", display(name))
        }
        return
    }
    let fd = openat(parent, name, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
    guard fd >= 0 else {
        throw Failure.posix("open", display(name))
    }
    defer { close(fd) }
    for child in try entries(of: fd) {
        try removeEntry(child, in: fd, device: device)
    }
    guard unlinkat(parent, name, AT_REMOVEDIR) == 0 else {
        throw Failure.posix("rmdir", display(name))
    }
}

/// The names in an open directory, without `.` and `..`, as
/// NUL-terminated bytes.
private func entries(of fd: Int32) throws -> [[CChar]] {
    let copy = dup(fd)
    guard copy >= 0 else {
        throw Failure.posix("dup", "")
    }
    guard let stream = fdopendir(copy) else {
        let failure = Failure.posix("fdopendir", "")
        close(copy)
        throw failure
    }
    defer { closedir(stream) }
    var names: [[CChar]] = []
    while let entry = readdir(stream) {
        let name = withUnsafeBytes(of: entry.pointee.d_name) { raw in
            raw.prefix(Int(entry.pointee.d_namlen)).map { CChar(bitPattern: $0) } + [0]
        }
        if name != cName(".") && name != cName("..") {
            names.append(name)
        }
    }
    return names
}
