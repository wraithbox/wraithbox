// C11-host-daemons, "Slot" and "Start interface": the slot checks before
// confinement, the deny probe, the argument refusal, the inherited
// descriptors, the lifeline, and the log format.
import Darwin
import Foundation
import Testing
import WraithBoxVM

@testable import WraithBoxVMD

@Suite("Slot checks")
struct SlotResolutionTests {
    @Test("the work slot's bundle resolves to its real path")
    func accepted() throws {
        let fixture = try BundleFixture()
        let resolved = try SlotResolution.resolve(slotValue: "work", home: fixture.home.path)
        #expect(resolved.slot == .work)
        #expect(resolved.realBundle == fixture.path)
    }

    @Test(
        "each slot value that isn't served is refused with its rule",
        arguments: [
            (String?.none, StartupRule.slotMissing),
            ("", .slotUnknown),
            ("Work", .slotUnknown),
            ("work ", .slotUnknown),
            ("build", .slotBuildUnsupported),
        ])
    func refusedSlot(value: String?, rule: StartupRule) throws {
        let fixture = try BundleFixture()
        try expectRefusal(rule) {
            try SlotResolution.resolve(slotValue: value, home: fixture.home.path)
        }
    }

    @Test("a missing bundle is refused with vmd-bundle-missing")
    func missingBundle() throws {
        let fixture = try BundleFixture()
        try expectRefusal(.bundleMissing) {
            try SlotResolution.resolve(slotValue: "isolated", home: fixture.home.path)
        }
    }

    @Test("a bundle that is a file is refused with vmd-bundle-missing")
    func bundleNotDirectory() throws {
        let home = try TemporaryDirectory()
        let data = HostPaths.data(home: home.path)
        try FileManager.default.createDirectory(
            atPath: data + "/vms", withIntermediateDirectories: true)
        FileManager.default.createFile(atPath: data + "/vms/macos-work.noindex", contents: Data())
        try expectRefusal(.bundleMissing) {
            try SlotResolution.resolve(slotValue: "work", home: home.path)
        }
    }

    @Test("a bundle that links to the other slot's bundle is refused with vmd-slot-bundle-mismatch")
    func linkedToOtherSlot() throws {
        let fixture = try BundleFixture()
        let data = HostPaths.data(home: fixture.home.path)
        try FileManager.default.createSymbolicLink(
            atPath: data + "/vms/macos-isolated.noindex", withDestinationPath: fixture.path)
        try expectRefusal(.slotBundleMismatch) {
            try SlotResolution.resolve(slotValue: "isolated", home: fixture.home.path)
        }
    }

    @Test("a vms directory that links elsewhere is refused with vmd-slot-bundle-mismatch")
    func vmsLinkedElsewhere() throws {
        let home = try TemporaryDirectory()
        let elsewhere = try TemporaryDirectory()
        let data = HostPaths.data(home: home.path)
        try FileManager.default.createDirectory(atPath: data, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(
            atPath: elsewhere.path + "/macos-work.noindex", withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(
            atPath: data + "/vms", withDestinationPath: elsewhere.path)
        try expectRefusal(.slotBundleMismatch) {
            try SlotResolution.resolve(slotValue: "work", home: home.path)
        }
    }

    @Test(
        "an empty or relative home is refused with paths-home-invalid", arguments: ["", "Users/x"])
    func invalidHome(home: String) throws {
        try expectRefusal(.homeInvalid) {
            try SlotResolution.resolve(slotValue: "work", home: home)
        }
    }

    @Test("the home directory comes from the user database, not HOME")
    func homeFromUserDatabase() throws {
        let saved = getenv("HOME").map { String(cString: $0) }
        setenv("HOME", "/nonexistent-home", 1)
        defer {
            if let saved {
                setenv("HOME", saved, 1)
            }
        }
        let home = SlotResolution.homeDirectory()
        #expect(home != "/nonexistent-home")
        #expect(home.hasPrefix("/"))
    }

    @Test("the cache directory is resolved and has no trailing slash")
    func cacheDirectory() throws {
        let cache = try SlotResolution.cacheDirectory()
        #expect(cache.hasPrefix("/private/var/folders/"))
        #expect(!cache.hasSuffix("/"))
    }
}

@Suite("Start interface")
struct StartInterfaceTests {
    @Test("no arguments are accepted, and any argument is refused with vmd-arguments")
    func arguments() throws {
        try StartInterface.checkArguments(["wb-vmd"])
        try expectRefusal(.arguments) {
            try StartInterface.checkArguments(["wb-vmd", "--bundle=/tmp/x"])
        }
    }

    @Test("the deny probe refuses when the probe file can be read")
    func probe() throws {
        // Unconfined, the probe file can be read, so the probe refuses.
        try expectRefusal(.sandboxProbe) {
            try Confinement.probe()
        }
        try Confinement.probe(path: "/nonexistent/probe")
    }

    @Test("the right descriptor kinds are accepted")
    func accepted() throws {
        let directory = try TemporaryDirectory()
        let pipeEnds = try makePipe()
        let listening = try listeningSocket(at: directory.path + "/s")
        let datagram = try makeSocketPair(SOCK_DGRAM)
        defer { closeAll([pipeEnds.0, pipeEnds.1, listening, datagram.0, datagram.1]) }
        try StartInterface.check(.lifeline, fd: pipeEnds.0)
        try StartInterface.check(.log, fd: pipeEnds.1)
        try StartInterface.check(.grpc, fd: listening)
        try StartInterface.check(.handoff, fd: datagram.0)
    }

    @Test("a wrong or missing descriptor is refused with vmd-descriptor-invalid")
    func refused() throws {
        let pipeEnds = try makePipe()
        let stream = try makeSocketPair(SOCK_STREAM)
        let datagram = try makeSocketPair(SOCK_DGRAM)
        let file = open("/dev/null", O_RDONLY)
        // Far above any descriptor the test process opens.
        let unused: Int32 = 9999
        defer {
            closeAll([pipeEnds.0, pipeEnds.1, stream.0, stream.1, datagram.0, datagram.1, file])
        }
        let cases: [(InheritedDescriptor, Int32)] = [
            (.lifeline, file), (.lifeline, unused), (.log, unused),
            (.grpc, pipeEnds.0), (.grpc, datagram.0), (.grpc, stream.0),
            (.handoff, stream.0), (.handoff, pipeEnds.1), (.handoff, unused),
        ]
        for (descriptor, fd) in cases {
            try expectRefusal(.descriptor) {
                try StartInterface.check(descriptor, fd: fd)
            }
        }
    }

    @Test("the lifeline reports end of file once its writer closes")
    func lifeline() async throws {
        let pipeEnds = try makePipe()
        defer { close(pipeEnds.0) }
        let (stream, continuation) = AsyncStream.makeStream(of: String.self)
        Lifeline.watch(fd: pipeEnds.0) { reason in
            continuation.yield(reason)
            continuation.finish()
        }
        _ = write(pipeEnds.1, "x", 1)
        close(pipeEnds.1)
        var reasons: [String] = []
        for await reason in stream {
            reasons.append(reason)
        }
        #expect(reasons == ["end of file"])
    }
}

@Suite("Log records")
struct EventLogTests {
    @Test("a refusal record has the time, level, event and rule first")
    func refusalRecord() throws {
        let recorded = RecordedLog()
        recorded.log.refusal(
            "rpc-refused", rule: "vm-generation", ["generation": .int(3), "b": .string("x")])
        #expect(
            recorded.lines == [
                #"{"time":"t","level":"warn","event":"rpc-refused","rule":"vm-generation","b":"x","generation":3}"#
            ])
    }

    @Test("control characters are escaped, so a value can't start a new line")
    func escaping() {
        #expect(EventLog.quote("a\nb\"c\\\u{1}") == #""a\nb\"c\\\u0001""#)
    }

    @Test("a long field is cut to the field bound")
    func cut() {
        let long = String(repeating: "é", count: 600)
        #expect(EventLog.cut(long).utf8.count <= EventLog.maxFieldBytes)
    }

    @Test("the time is RFC 3339 in UTC")
    func time() {
        let now = EventLog.utcNow()
        #expect(now.count == "2026-10-16T19:00:00.000000Z".count)
        #expect(now.hasSuffix("Z"))
    }
}

/// Expects `body` to throw a startup refusal with `rule`.
func expectRefusal<T>(
    _ rule: StartupRule, sourceLocation: SourceLocation = #_sourceLocation,
    _ body: () throws -> T
) throws {
    do {
        _ = try body()
        Issue.record(
            "expected \(rule.rawValue), nothing was refused", sourceLocation: sourceLocation)
    } catch let refusal as StartupRefusal {
        #expect(refusal.rule == rule, sourceLocation: sourceLocation)
    }
}

func makePipe() throws -> (Int32, Int32) {
    var fds: [Int32] = [0, 0]
    guard pipe(&fds) == 0 else {
        throw POSIXError(.EMFILE)
    }
    return (fds[0], fds[1])
}

func makeSocketPair(_ type: Int32) throws -> (Int32, Int32) {
    var fds: [Int32] = [0, 0]
    guard socketpair(AF_UNIX, type, 0, &fds) == 0 else {
        throw POSIXError(.EMFILE)
    }
    return (fds[0], fds[1])
}

func closeAll(_ fds: [Int32]) {
    for fd in fds {
        close(fd)
    }
}
