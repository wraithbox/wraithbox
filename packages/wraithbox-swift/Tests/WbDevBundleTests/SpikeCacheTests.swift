// The path checks of wb-dev-bundle (I350): only paths in the spike cache,
// no symbolic link, nothing in or under a directory with a KEEP file, and
// never restore-images/.
import Foundation
import Testing

@testable import wb_dev_bundle

/// The rule of the refusal `body` throws, or nil when it throws none or
/// another error.
private func ruleOf(_ body: () throws -> Void) -> Rule? {
    do {
        try body()
        return nil
    } catch {
        return (error as? Refusal)?.rule
    }
}

@Suite("wb-dev-bundle: paths in the spike cache")
struct SpikeCacheTests {
    @Test("a relative path is taken relative to the cache, and an absolute one inside it is kept")
    func accepted() throws {
        let test = try TestCache()
        try test.makeDirectory("x50/base")
        let relative = try test.cache.bundle("x50/base", for: .change)
        let absolute = try test.cache.bundle(test.path("x50/base/"), for: .change)
        #expect(relative == absolute)
        #expect(relative.path == test.path("x50/base"))
        #expect(relative.components == ["x50", "base"])
        #expect(
            try test.cache.bundle("x50/new/deeper", for: .create).path
                == test.path("x50/new/deeper"))
    }

    @Test(
        "a path outside the cache is refused",
        arguments: ["/tmp/x50", "/", "/Users/Shared/wraithbox-spikes/x50"])
    func outside(argument: String) throws {
        let test = try TestCache()
        #expect(ruleOf { _ = try test.cache.bundle(argument, for: .create) } == .outsideCache)
    }

    @Test("the cache itself, and a path that is next to it, are refused")
    func cacheItselfAndNeighbor() throws {
        let test = try TestCache()
        #expect(ruleOf { _ = try test.cache.bundle(test.root, for: .read) } == .outsideCache)
        #expect(
            ruleOf { _ = try test.cache.bundle(test.root + "-x/a", for: .create) } == .outsideCache)
        #expect(ruleOf { _ = try test.cache.bundle(test.root + "/", for: .read) } == .pathComponent)
    }

    @Test(
        "an empty, . or .. component is refused",
        arguments: ["", "..", "../x50", "x50/../../etc", "x50//base", "./x50", "x50/./base"])
    func components(argument: String) throws {
        let test = try TestCache()
        #expect(ruleOf { _ = try test.cache.bundle(argument, for: .create) } == .pathComponent)
    }

    @Test("a symbolic link as the bundle, or above it, is refused")
    func symbolicLinks() throws {
        let test = try TestCache()
        try test.makeDirectory("x50/base")
        try test.link("x50/link", to: test.path("x50/base"))
        try test.link("away", to: test.base)
        for use in [PathUse.create, .change, .read] {
            #expect(ruleOf { _ = try test.cache.bundle("x50/link", for: use) } == .symbolicLink)
            #expect(ruleOf { _ = try test.cache.bundle("away/new", for: use) } == .symbolicLink)
        }
        #expect(
            ruleOf { _ = try test.cache.bundle("x50/link/inner", for: .create) } == .symbolicLink)
    }

    @Test("a cache directory that is a symbolic link, or missing, is refused")
    func cacheRoot() throws {
        let test = try TestCache()
        let linked = SpikeCache(root: test.base + "/linked")
        try FileManager.default.createSymbolicLink(
            atPath: linked.root, withDestinationPath: test.root)
        #expect(ruleOf { _ = try linked.bundle("x50", for: .create) } == .cacheRoot)
        let missing = SpikeCache(root: test.base + "/missing")
        #expect(ruleOf { _ = try missing.bundle("x50", for: .create) } == .cacheRoot)
    }

    @Test("restore-images/ is never a bundle", arguments: [PathUse.create, .change, .read])
    func restoreImages(use: PathUse) throws {
        let test = try TestCache()
        #expect(ruleOf { _ = try test.cache.bundle("restore-images", for: use) } == .restoreImages)
        #expect(
            ruleOf { _ = try test.cache.bundle("restore-images/b", for: use) } == .restoreImages)
    }

    @Test("a KEEP file in the bundle or above it refuses a change, and not a read")
    func keep() throws {
        let test = try TestCache()
        try test.makeDirectory("x08/base")
        try test.write("x08/KEEP", "#228: X08 data disk follow-up\nmore")
        #expect(ruleOf { _ = try test.cache.bundle("x08/base", for: .change) } == .keepFile)
        #expect(ruleOf { _ = try test.cache.bundle("x08", for: .change) } == .keepFile)
        #expect(ruleOf { _ = try test.cache.bundle("x08/new", for: .create) } == .keepFile)
        #expect(try test.cache.bundle("x08/base", for: .read).path == test.path("x08/base"))
        do {
            _ = try test.cache.bundle("x08/base", for: .change)
        } catch {
            #expect(error.reason.hasSuffix("KEEP says: #228: X08 data disk follow-up"))
        }
    }

    @Test("a KEEP file that isn't a regular file still keeps the directory")
    func keepNotAFile() throws {
        let test = try TestCache()
        try test.makeDirectory("x09/KEEP")
        #expect(ruleOf { _ = try test.cache.bundle("x09", for: .change) } == .keepFile)
    }

    @Test("a new bundle's path is free, and a changed one is a directory")
    func existence() throws {
        let test = try TestCache()
        try test.write("x50/file")
        #expect(ruleOf { _ = try test.cache.bundle("x50", for: .create) } == .targetExists)
        #expect(ruleOf { _ = try test.cache.bundle("x51", for: .change) } == .missing)
        #expect(ruleOf { _ = try test.cache.bundle("x51", for: .read) } == .missing)
        #expect(ruleOf { _ = try test.cache.bundle("x50/file", for: .change) } == .notDirectory)
        #expect(ruleOf { _ = try test.cache.bundle("x50/file/b", for: .create) } == .notDirectory)
    }

    @Test("a restore image is a regular file in restore-images/")
    func restoreImage() throws {
        let test = try TestCache()
        try test.write("restore-images/UniversalMac.ipsw")
        try test.write("x50/UniversalMac.ipsw")
        try test.makeDirectory("restore-images/dir")
        try test.link("restore-images/link.ipsw", to: test.path("x50/UniversalMac.ipsw"))
        let image = try test.cache.restoreImage("restore-images/UniversalMac.ipsw")
        #expect(image.path == test.path("restore-images/UniversalMac.ipsw"))
        #expect(
            ruleOf { _ = try test.cache.restoreImage("x50/UniversalMac.ipsw") } == .restoreImagePath
        )
        #expect(ruleOf { _ = try test.cache.restoreImage("restore-images") } == .restoreImagePath)
        #expect(
            ruleOf { _ = try test.cache.restoreImage("restore-images/dir") } == .restoreImagePath)
        #expect(ruleOf { _ = try test.cache.restoreImage("restore-images/none.ipsw") } == .missing)
        #expect(
            ruleOf { _ = try test.cache.restoreImage("restore-images/link.ipsw") } == .symbolicLink)
        #expect(
            ruleOf { _ = try test.cache.restoreImage("/tmp/UniversalMac.ipsw") } == .outsideCache)
    }

    @Test("the parents of a new bundle are made as directories in the cache")
    func parents() throws {
        let test = try TestCache()
        let target = try test.cache.bundle("i350/a/bundle", for: .create)
        try test.cache.makeParents(of: target)
        #expect(test.exists("i350/a"))
        #expect(!test.exists("i350/a/bundle"))
    }
}
