// `clone` and `clean` (I350): an APFS clone of a bundle, and the removal of
// a directory in the cache. Neither touches a bundle whose VM runs.
import Foundation

/// Makes `target` an APFS clone of `source` with clonefile(2), which
/// copies symbolic links in the bundle as links. Refuses while the
/// source's VM runs, because its disks change under the clone.
func cloneBundle(
    from source: CachePath, to target: CachePath, cache: SpikeCache, log: Log
) throws {
    if let pid = BundleLock.holder(bundle: source.path) {
        throw Refusal(.vmRunning, source.path, "process \(pid) runs its VM")
    }
    try cache.makeParents(of: target)
    log.step("cloning \(source.path) to \(target.path)")
    guard clonefile(source.path, target.path, UInt32(CLONE_NOFOLLOW)) == 0 else {
        throw Failure.posix("clonefile to", target.path)
    }
    log.step("cloned \(target.path)")
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
/// cache, such as a disk image's volume mounted in a bundle, or when a VM
/// runs from a bundle in it. Symbolic links in the tree are removed, never
/// followed.
func cleanBundle(_ target: CachePath, cache: SpikeCache, log: Log, lstat: Lstat = realLstat)
    throws
{
    guard let rootInfo = lstat(cache.root) else {
        throw Failure.posix("lstat", cache.root)
    }
    let bytes = try walkForRemoval(target.path, device: rootInfo.st_dev, lstat: lstat)
    log.step("removing \(target.path)")
    try FileManager.default.removeItem(atPath: target.path)
    log.step(
        "removed \(target.path), \(bytes >> 20) MiB of allocated blocks"
            + " (APFS clones can share some of them)")
}

/// The allocated bytes under `path`, or a refusal.
private func walkForRemoval(_ path: String, device: dev_t, lstat: Lstat) throws -> Int64 {
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
    if let pid = BundleLock.holder(bundle: path) {
        throw Refusal(.vmRunning, path, "process \(pid) runs its VM")
    }
    for name in try FileManager.default.contentsOfDirectory(atPath: path) {
        bytes += try walkForRemoval(path + "/" + name, device: device, lstat: lstat)
    }
    return bytes
}
