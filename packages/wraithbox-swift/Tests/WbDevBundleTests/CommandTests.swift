// The commands of wb-dev-bundle (I350) up to the point where one would
// start a VM: each refusal is logged with its rule and changes nothing,
// and clone, clean and stop do their work in a throwaway cache.
// `provision` refuses a macOS older than 27 first, so on such a host only
// that refusal is tested for it (HostRequirement).
import Foundation
import Testing
import WraithBoxVM

@testable import wb_dev_bundle

private let hostSupported = HostRequirement.supports(
    ProcessInfo.processInfo.operatingSystemVersion)

@Suite("wb-dev-bundle: commands", .serialized)
@MainActor
struct CommandTests {
    @Test(
        "the command line is a command and its arguments",
        arguments: [
            (
                ["install", "a", "restore-images/i.ipsw"],
                Command.install(bundle: "a", image: "restore-images/i.ipsw")
            ),
            (["clone", "a", "b"], .clone(source: "a", target: "b")),
            (["provision", "a"], .provision(bundle: "a", minutes: 60)),
            (["provision", "a", "15"], .provision(bundle: "a", minutes: 15)),
            (["stop", "a"], .stop(bundle: "a")),
            (["clean", "a"], .clean(bundle: "a")),
        ])
    func parse(arguments: [String], command: Command) throws {
        #expect(try Command.parse(arguments) == command)
    }

    @Test(
        "anything else is a usage error, with status 2",
        arguments: [
            [], ["boot", "a"], ["install", "a"], ["clone", "a"], ["stop"], ["clean", "a", "b"],
            ["provision", "a", "0"], ["provision", "a", "1441"], ["provision", "a", "ten"],
        ])
    func usage(arguments: [String]) async throws {
        let test = try TestCache()
        #expect(await Command.run(arguments, cache: test.cache, log: test.log) == 2)
        #expect(test.lines.refused(by: .usage))
    }

    @Test(
        "each refusal is logged with its rule, and changes nothing",
        arguments: [
            (["clone", "x50/base", "/tmp/elsewhere"], Rule.outsideCache),
            (["clone", "x50/base", "x08/clone"], .keepFile),
            (["clone", "x50/link", "x50/clone"], .symbolicLink),
            (["clone", "x50/base", "x50/base"], .targetExists),
            (["install", "x50/new", "x50/base/hw.bin"], .restoreImagePath),
            (["install", "restore-images/new", "restore-images/i.ipsw"], .restoreImages),
            (["provision", "x08/base"], .keepFile),
            (["clean", "x08"], .keepFile),
            (["clean", "restore-images"], .restoreImages),
            (["clean", "x50/link"], .symbolicLink),
            (["clean", "../x50"], .pathComponent),
            (["stop", "x50/none"], .missing),
            (["clean", "Restore-Images"], .restoreImages),
            (["clean", "RESTORE-IMAGES/x"], .restoreImages),
            (["clean", "X08"], .nameSpelling),
            (["clean", "X08/base"], .nameSpelling),
            (["provision", "X08/base"], .nameSpelling),
            (["stop", "Restore-Images"], .restoreImages),
            (["clean", "dev"], .keepFile),
        ])
    func refusals(arguments: [String], rule: Rule) async throws {
        try await expectRefusal(arguments, rule: rule)
    }

    @Test(
        "provision refuses a bundle that isn't installed or has booted",
        .enabled(if: hostSupported, "provision refuses a macOS older than 27 first"),
        arguments: [
            (["provision", "x50/base"], Rule.notInstalled),
            (["provision", "x50/booted"], .alreadyBooted),
        ])
    func provisionRefusals(arguments: [String], rule: Rule) async throws {
        try await expectRefusal(arguments, rule: rule)
    }

    @Test(
        "provision on a macOS older than 27 is refused before the bundle is read or locked",
        .enabled(if: !hostSupported, "the host runs macOS 27 or later"),
        arguments: [["provision", "x50/base"], ["provision", "x50/booted"]])
    func provisionRefusedHost(arguments: [String]) async throws {
        try await expectRefusal(arguments, rule: .hostUnsupported)
    }

    /// Runs the command in a cache with one of each kind of bundle, and
    /// expects it to be refused with the rule and to change nothing.
    private func expectRefusal(_ arguments: [String], rule: Rule) async throws {
        let test = try TestCache()
        try test.write("x50/base/hw.bin")
        try test.installedBundle("x50/booted", booted: true)
        try test.link("x50/link", to: test.path("x50/base"))
        try test.installedBundle("x08/base")
        try test.write("x08/KEEP", "#228: X08 data disk follow-up")
        try test.write("restore-images/i.ipsw")
        try test.installedBundle("dev/base")
        try test.write("dev/base/KEEP", "#277: fixture bundle")
        let before = try snapshot(test)
        #expect(await Command.run(arguments, cache: test.cache, log: test.log) == 1)
        #expect(test.lines.refused(by: rule), "\(test.lines.all)")
        #expect(try snapshot(test) == before)
    }

    @Test("clone makes an APFS clone with the same files, and its parents")
    func clone() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        #expect(
            await Command.run(
                ["clone", "x50/base", "i350/a/clone"], cache: test.cache, log: test.log) == 0)
        let record = try BundleRecord.read(bundle: test.path("i350/a/clone"))
        #expect(record.buildVersion == "26A434")
        #expect(test.exists("i350/a/clone/sys.wbdisk"))
        #expect(test.lines.all.contains { $0.contains("cloned \(test.path("i350/a/clone"))") })
    }

    @Test("a bundle whose VM runs is not cloned, provisioned or removed")
    func running() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        let lock = try BundleLock.take(bundle: test.path("x50/base"))
        for arguments in [
            ["clone", "x50/base", "x50/c"], ["provision", "x50/base"], ["clean", "x50"],
        ] {
            #expect(await Command.run(arguments, cache: test.cache, log: test.log) == 1)
        }
        // An older host refuses provision before it looks at the lock.
        #expect(
            test.lines.all.filter { $0.hasSuffix("(rule: vm-running)") }.count
                == (hostSupported ? 3 : 2))
        #expect(hostSupported || test.lines.refused(by: .hostUnsupported))
        #expect(test.exists("x50/base/sys.wbdisk"))
        #expect(!test.exists("x50/c"))
        withExtendedLifetime(lock) {}
    }

    @Test("a clone has no lock file, so it names no process")
    func cloneWithoutLock() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        _ = try BundleLock.take(bundle: test.path("x50/base"))
        #expect(test.exists("x50/base/\(BundleLock.fileName)"))
        #expect(
            await Command.run(["clone", "x50/base", "x50/c"], cache: test.cache, log: test.log) == 0
        )
        #expect(!test.exists("x50/c/\(BundleLock.fileName)"))
        #expect(BundleLock.holder(bundle: test.path("x50/base")) == nil)
    }

    @Test("the VM's bundle files must be regular files")
    func machineFiles() throws {
        let test = try TestCache()
        for name in ["hw.bin", "mid.bin", "aux.wbstore", "data.wbdisk"] {
            try test.write("x50/base/" + name)
        }
        try test.write("x51/sys.wbdisk")
        try test.link("x50/base/sys.wbdisk", to: test.path("x51/sys.wbdisk"))
        #expect(throws: Refusal.self) { try checkMachineFiles(test.path("x50/base")) }
        try FileManager.default.removeItem(atPath: test.path("x50/base/sys.wbdisk"))
        try test.write("x50/base/sys.wbdisk")
        try checkMachineFiles(test.path("x50/base"))
    }

    @Test("clean removes the directory and logs the space")
    func clean() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        try test.write("x50/other/file")
        #expect(await Command.run(["clean", "x50/base"], cache: test.cache, log: test.log) == 0)
        #expect(!test.exists("x50/base"))
        #expect(test.exists("x50/other/file"))
        #expect(test.lines.all.contains { $0.contains("removed \(test.path("x50/base"))") })
    }

    @Test("clean removes a symbolic link in the tree and not what it points to")
    func cleanLinkInside() async throws {
        let test = try TestCache()
        try test.write("x51/kept")
        try test.makeDirectory("x50/base")
        try test.link("x50/base/out", to: test.path("x51"))
        #expect(await Command.run(["clean", "x50"], cache: test.cache, log: test.log) == 0)
        #expect(!test.exists("x50"))
        #expect(test.exists("x51/kept"))
    }

    @Test("clean refuses a tree with an entry on another device, and removes nothing")
    func cleanOtherDevice() throws {
        let test = try TestCache()
        try test.write("x50/base/datavol/file")
        let mounted: Lstat = { path in
            guard var info = realLstat(path) else {
                return nil
            }
            if path.split(separator: "/").contains("datavol") {
                info.st_dev += 1
            }
            return info
        }
        let target = try test.cache.bundle("x50", for: .change)
        let refusal = #expect(throws: Refusal.self) {
            try cleanBundle(target, cache: test.cache, log: test.log, lstat: mounted)
        }
        #expect(refusal?.rule == .otherDevice)
        #expect(test.exists("x50/base/datavol/file"))
    }

    @Test("stop with no VM running says so and succeeds")
    func stopNothing() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        #expect(await Command.run(["stop", "x50/base"], cache: test.cache, log: test.log) == 0)
        #expect(test.lines.all.contains { $0.hasSuffix("no VM runs from it") })
        #expect(!test.exists("x50/base/\(BundleLock.fileName)"))
    }

    @Test("stop signals the process in the lock file and waits for the lock")
    func stopRunning() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        var lock: BundleLock? = try BundleLock.take(bundle: test.path("x50/base"))
        try test.setLockHolder("x50/base", 424_242)
        var signaled: [pid_t] = []
        let status = await stopBundle(
            try test.cache.bundle("x50/base", for: .read), log: test.log, wait: .seconds(5)
        ) { pid in
            signaled.append(pid)
            lock = nil
            return 0
        }
        #expect(status == 0)
        #expect(signaled == [424_242])
        #expect(lock == nil)
    }

    @Test("stop gives up when the process keeps the lock")
    func stopTimeout() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        let lock = try BundleLock.take(bundle: test.path("x50/base"))
        try test.setLockHolder("x50/base", 424_242)
        let status = await stopBundle(
            try test.cache.bundle("x50/base", for: .read), log: test.log, wait: .milliseconds(300)
        ) { _ in 0 }
        #expect(status == 1)
        #expect(test.lines.all.last?.contains("still runs its VM") == true)
        withExtendedLifetime(lock) {}
    }

    @Test("stop never signals this process or pid 1")
    func stopOwnProcess() async throws {
        let test = try TestCache()
        try test.installedBundle("x50/base")
        let lock = try BundleLock.take(bundle: test.path("x50/base"))
        var signaled = false
        let status = await stopBundle(
            try test.cache.bundle("x50/base", for: .read), log: test.log, wait: .milliseconds(100)
        ) { _ in
            signaled = true
            return 0
        }
        #expect(status == 1)
        #expect(!signaled)
        withExtendedLifetime(lock) {}
    }

    /// Every path in the cache with its file type, to show that a refused
    /// command changed nothing.
    private func snapshot(_ test: TestCache) throws -> [String: mode_t] {
        var result: [String: mode_t] = [:]
        let names = FileManager.default.enumerator(atPath: test.root)
        while let name = names?.nextObject() as? String {
            var info = stat()
            lstat(test.path(name), &info)
            result[name] = info.st_mode & S_IFMT
        }
        return result
    }
}

@Suite("wb-dev-bundle: lock, record and provisioning values")
struct BundleTests {
    @Test("the lock is exclusive, names its holder, and goes with its descriptor")
    func lock() throws {
        let test = try TestCache()
        try test.makeDirectory("x50/base")
        let bundle = test.path("x50/base")
        #expect(BundleLock.holder(bundle: bundle) == nil)
        var lock: BundleLock? = try BundleLock.take(bundle: bundle)
        #expect(BundleLock.holder(bundle: bundle) == getpid())
        #expect(throws: Refusal(.vmRunning, bundle, "process \(getpid()) runs its VM")) {
            _ = try BundleLock.take(bundle: bundle)
        }
        lock = nil
        #expect(lock == nil)
        #expect(BundleLock.holder(bundle: bundle) == nil)
        _ = try BundleLock.take(bundle: bundle)
    }

    @Test("the record survives a round trip")
    func record() throws {
        let test = try TestCache()
        try test.installedBundle("x50/base", booted: true)
        let record = try BundleRecord.read(bundle: test.path("x50/base"))
        #expect(record.bootedAt == Date(timeIntervalSince1970: 60))
        #expect(record.cpuCount == 4)
    }

    @Test(
        "a MAC address is written as bootpd writes it in its leases",
        arguments: [
            ("0a:01:00:ff:10:00", "a:1:0:ff:10:0"), ("12:34:56:78:9a:bc", "12:34:56:78:9a:bc"),
        ])
    func leaseAddresses(mac: String, lease: String) {
        #expect(leaseAddress(mac) == lease)
    }

    @Test("a password is 24 hexadecimal digits, new each time")
    func passwords() {
        let first = randomPassword()
        #expect(first.count == 24)
        let hexadecimal = first.allSatisfy { $0.isHexDigit }
        #expect(hexadecimal)
        #expect(first != randomPassword())
    }
}
