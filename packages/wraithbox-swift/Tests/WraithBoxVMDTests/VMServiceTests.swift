// C11-host-daemons, "VMService": the handlers with a fake VM. Each
// refusal comes back with its rule as the ErrorInfo reason, and is logged
// with it: vm-resources, vm-already-running, vm-generation, vm-stop-mode,
// and a device-set refusal passed through.
import Foundation
import GRPCCore
import GRPCInProcessTransport
import GRPCProtobuf
import Synchronization
import Testing
import WraithBoxRPC
import WraithBoxVM

@testable import WraithBoxVMD

/// A VMService over the in-process transport, on a fake engine and a
/// bundle with every bundle file.
@MainActor
final class ServiceFixture {
    private let bundle: BundleFixture
    let engine = FakeEngine()
    nonisolated let recorded = RecordedLog()
    nonisolated let supervisor: VMSupervisor
    nonisolated let connector: GuestConnector
    nonisolated let bundlePath: String
    nonisolated let homePath: String
    /// wb-vmd's end of the hand-off channel, and wb-hostd's, which the
    /// tests read.
    nonisolated let handoff: (vmd: Int32, host: Int32)

    init(connectTimeout: Duration = GuestConnector.defaultTimeout) throws {
        bundle = try BundleFixture()
        bundlePath = bundle.path
        homePath = bundle.home.path
        let pair = try makeSocketPair(SOCK_DGRAM)
        handoff = (pair.0, pair.1)
        supervisor = VMSupervisor(
            engine: engine, slot: .work, bundlePath: bundle.path,
            handoff: HandoffChannel(fd: pair.0), log: recorded.log)
        connector = GuestConnector(
            supervisor: supervisor, handoff: HandoffChannel(fd: pair.0), log: recorded.log,
            timeout: connectTimeout)
    }

    /// Whether the test closed wb-hostd's end already.
    private nonisolated let hostClosed = Mutex(false)

    deinit {
        close(handoff.vmd)
        if !hostClosed.withLock({ $0 }) {
            close(handoff.host)
        }
    }

    /// Closes wb-hostd's end, as when wb-hostd is gone.
    nonisolated func closeHostEnd() {
        hostClosed.withLock { closed in
            if !closed {
                close(handoff.host)
                closed = true
            }
        }
    }

    /// Runs `body` with a client of the service.
    nonisolated func withClient<T: Sendable>(
        _ body: (Wraithbox_Vmd_V1_VMService.Client<InProcessTransport.Client>) async throws -> T
    ) async throws -> T {
        let transport = InProcessTransport()
        let service = VMServiceHandler(
            supervisor: supervisor, connector: connector, log: recorded.log)
        return try await withGRPCServer(transport: transport.server, services: [service]) { _ in
            try await withGRPCClient(transport: transport.client) { grpc in
                try await body(Wraithbox_Vmd_V1_VMService.Client(wrapping: grpc))
            }
        }
    }
}

func startRequest(
    generation: UInt64, cpuCount: UInt32 = 4, memoryMiB: UInt64 = 4096, mac: [UInt8] = testMAC
) -> Wraithbox_Vmd_V1_StartRequest {
    var request = Wraithbox_Vmd_V1_StartRequest()
    request.generation = generation
    request.cpuCount = cpuCount
    request.memoryMib = memoryMiB
    request.macAddress = Data(mac)
    return request
}

func stopRequest(generation: UInt64, mode: Wraithbox_Vmd_V1_StopMode)
    -> Wraithbox_Vmd_V1_StopRequest
{
    var request = Wraithbox_Vmd_V1_StopRequest()
    request.generation = generation
    request.mode = mode
    return request
}

/// Runs `call`, expects a refusal, and returns its code and ErrorInfo
/// reason.
func refusal(_ call: () async throws -> Void) async -> (RPCError.Code?, String?) {
    do {
        try await call()
        return (nil, nil)
    } catch {
        return ((error as? RPCError)?.code, refusalReason(error))
    }
}

@Suite("VMService")
struct VMServiceTests {
    @Test("Start boots the fixed device set, logs it, and WatchState sees running")
    func startAndWatch() async throws {
        let fixture = try await ServiceFixture()
        let states = try await fixture.withClient { client in
            _ = try await client.start(startRequest(generation: 1))
            return try await client.watchState(Wraithbox_Vmd_V1_WatchStateRequest()) { response in
                var seen: [Wraithbox_Vmd_V1_State] = []
                for try await message in response.messages {
                    seen.append(message.state.state)
                    break
                }
                return seen
            }
        }
        #expect(states == [.running])
        #expect(await fixture.engine.requests.count == 1)
        let request = try #require(await fixture.engine.requests.first)
        #expect(
            request.description
                == VMDescription.fixedSet(bundlePath: fixture.bundlePath, slot: .work))
        #expect(request.cpuCount == 4)
        #expect(request.memoryBytes == 4096 << 20)
        let devices = try #require(
            fixture.recorded.events("vm-devices").first?["devices"] as? [String])
        #expect(
            devices == [
                "vsock", "entropy", "disk:system:rw", "disk:data:rw", "disk:tools:ro",
                "mac-graphics:1",
                "usb-keyboard", "usb-pointer", "network:file-handle",
            ])
    }

    /// CPUs and MiB: zero, over the fake engine's 8 CPUs, under its 512
    /// MiB, over its 16 GiB, and a size that overflows in bytes.
    nonisolated static let resourceCases: [(UInt32, UInt64)] = [
        (0, 4096), (4, 0), (9, 4096), (4, 256), (4, 17 * 1024), (4, UInt64.max),
    ]

    @Test(
        "resources of 0 or outside the host's range are refused with vm-resources",
        arguments: resourceCases)
    func resources(cpuCount: UInt32, memoryMiB: UInt64) async throws {
        let fixture = try await ServiceFixture()
        let (code, reason) = await refusal {
            try await fixture.withClient { client in
                _ = try await client.start(
                    startRequest(generation: 1, cpuCount: cpuCount, memoryMiB: memoryMiB))
            }
        }
        #expect(code == .invalidArgument)
        #expect(reason == "vm-resources")
        #expect(fixture.recorded.rules == ["vm-resources"])
        #expect(await fixture.engine.requests.isEmpty)
    }

    @Test("Start while the VM runs is refused with vm-already-running")
    func alreadyRunning() async throws {
        let fixture = try await ServiceFixture()
        let (code, reason) = await refusal {
            try await fixture.withClient { client in
                _ = try await client.start(startRequest(generation: 1))
                _ = try await client.start(startRequest(generation: 2))
            }
        }
        #expect(code == .failedPrecondition)
        #expect(reason == "vm-already-running")
        #expect(fixture.recorded.rules == ["vm-already-running"])
        #expect(await fixture.engine.requests.count == 1)
    }

    @Test(
        "generation 0, an old generation, and a stop for another generation are refused with vm-generation"
    )
    func generations() async throws {
        let fixture = try await ServiceFixture()
        try await fixture.withClient { client in
            let zero = await refusal { _ = try await client.start(startRequest(generation: 0)) }
            #expect(zero == (.invalidArgument, "vm-generation"))
            _ = try await client.start(startRequest(generation: 5))
            let other = await refusal {
                _ = try await client.stop(stopRequest(generation: 4, mode: .force))
            }
            #expect(other == (.failedPrecondition, "vm-generation"))
            _ = try await client.stop(stopRequest(generation: 5, mode: .force))
            let old = await refusal { _ = try await client.start(startRequest(generation: 5)) }
            #expect(old == (.failedPrecondition, "vm-generation"))
            _ = try await client.start(startRequest(generation: 6))
        }
        #expect(fixture.recorded.rules == ["vm-generation", "vm-generation", "vm-generation"])
        #expect(await fixture.engine.requests.count == 2)
    }

    @Test("a stop without a mode is refused with vm-stop-mode; each mode reaches the VM once")
    func stopModes() async throws {
        let fixture = try await ServiceFixture()
        try await fixture.withClient { client in
            _ = try await client.start(startRequest(generation: 1))
            let none = await refusal {
                _ = try await client.stop(stopRequest(generation: 1, mode: .unspecified))
            }
            #expect(none == (.invalidArgument, "vm-stop-mode"))
            _ = try await client.stop(stopRequest(generation: 1, mode: .request))
            // The guest was asked already: a second request doesn't reach it.
            _ = try await client.stop(stopRequest(generation: 1, mode: .request))
            _ = try await client.stop(stopRequest(generation: 1, mode: .force))
            let state = try await client.getState(Wraithbox_Vmd_V1_GetStateRequest())
            #expect(state.state.state == .stopped)
            #expect(state.state.generation == 1)
            // A stopped VM stops again without an error.
            _ = try await client.stop(stopRequest(generation: 1, mode: .force))
        }
        #expect(await fixture.engine.machines.first?.stops == ["request", "force"])
        #expect(fixture.recorded.rules == ["vm-stop-mode"])
    }

    @Test(
        "a device-set refusal passes through with its rule, and nothing is built",
        arguments: [
            (BundleFile.dataDisk, "symlink", DeviceSetRule.fileOutsideBundle),
            (BundleFile.toolsDisk, "remove", DeviceSetRule.bundleFileMissing),
            (BundleFile.systemDisk, "link", DeviceSetRule.fileOutsideBundle),
        ])
    func deviceSet(file: BundleFile, change: String, rule: DeviceSetRule) async throws {
        let fixture = try await ServiceFixture()
        let path = fixture.bundlePath + "/" + file.rawValue
        let outside = fixture.homePath + "/outside"
        FileManager.default.createFile(atPath: outside, contents: Data("x".utf8))
        try FileManager.default.removeItem(atPath: path)
        switch change {
        case "symlink":
            try FileManager.default.createSymbolicLink(atPath: path, withDestinationPath: outside)
        case "link": try FileManager.default.linkItem(atPath: outside, toPath: path)
        default: break
        }
        let (code, reason) = await refusal {
            try await fixture.withClient { client in
                _ = try await client.start(startRequest(generation: 1))
            }
        }
        #expect(code == .failedPrecondition)
        #expect(reason == rule.rawValue)
        #expect(fixture.recorded.rules == [rule.rawValue])
        #expect(await fixture.engine.requests.isEmpty)
    }

    @Test("a configuration the framework refuses is refused with vm-config, and the state says why")
    func configFailure() async throws {
        let fixture = try await ServiceFixture()
        await MainActor.run {
            fixture.engine.configFailure = VMFailure(
                code: 2, failureReason: "bad", description: "invalid")
        }
        let state = try await fixture.withClient { client in
            let refused = await refusal { _ = try await client.start(startRequest(generation: 1)) }
            #expect(refused == (.failedPrecondition, "vm-config"))
            return try await client.getState(Wraithbox_Vmd_V1_GetStateRequest()).state
        }
        #expect(state.state == .error)
        #expect(state.error.code == 2)
        #expect(state.error.failureReason == "bad")
        #expect(state.error.description_p == "invalid")
    }

    @Test("a guest that stops on its own is reported, and the next start is allowed")
    func guestStops() async throws {
        let fixture = try await ServiceFixture()
        try await fixture.withClient { client in
            _ = try await client.start(startRequest(generation: 1))
            await fixture.engine.machines.first?.guestStopped()
            let state = try await client.getState(Wraithbox_Vmd_V1_GetStateRequest())
            #expect(state.state.state == .stopped)
            _ = try await client.start(startRequest(generation: 2))
        }
        #expect(fixture.recorded.rules.isEmpty)
    }

    @Test("WatchState streams are capped with vm-watch-limit, and an ended one frees its slot")
    func watchLimit() async throws {
        let fixture = try await ServiceFixture()
        var watches: [StateWatch] = []
        for _ in 0..<VMSupervisor.maxWatchers {
            watches.append(try await fixture.supervisor.watch())
        }
        await #expect(throws: VMRefusal(.watchLimit, .resourceExhausted, "4 streams are open")) {
            _ = try await fixture.supervisor.watch()
        }
        watches[0].end()
        for _ in 0..<100 {
            if (try? await fixture.supervisor.watch()) != nil {
                return
            }
            try await Task.sleep(for: .milliseconds(10))
        }
        Issue.record("the ended stream's slot was not freed")
    }

    @Test("a Stop while the VM starts is refused with vm-starting, and the start completes")
    func stopWhileStarting() async throws {
        let fixture = try await ServiceFixture()
        let supervisor = fixture.supervisor
        await MainActor.run { fixture.engine.holdStart = true }
        let start = Task {
            try await supervisor.start(
                generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
        }
        let machine = try await heldMachine(fixture)
        await #expect(throws: VMRefusal(.starting, .failedPrecondition, "the VM is starting")) {
            try await supervisor.stop(generation: 1, mode: .force)
        }
        await machine.releaseStart()
        try await start.value
        #expect(await supervisor.status.state == .running)
        #expect(await machine.stops.isEmpty)
    }

    @Test("the lifeline's stop during a start waits for it, then forces the VM off")
    func stopForExitWhileStarting() async throws {
        let fixture = try await ServiceFixture()
        let supervisor = fixture.supervisor
        await MainActor.run { fixture.engine.holdStart = true }
        let start = Task {
            try await supervisor.start(
                generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
        }
        let machine = try await heldMachine(fixture)
        let exit = Task { await supervisor.stopForExit() }
        try await Task.sleep(for: .milliseconds(100))
        await machine.releaseStart()
        try await start.value
        await exit.value
        #expect(await machine.stops == ["force"])
        #expect(await supervisor.status.state == .stopped)
    }

    /// The fake VM once its start is held.
    private func heldMachine(_ fixture: ServiceFixture) async throws -> FakeVM {
        for _ in 0..<200 {
            if let machine = await fixture.engine.machines.first, await machine.isHeld {
                return machine
            }
            try await Task.sleep(for: .milliseconds(5))
        }
        throw POSIXError(.ETIMEDOUT)
    }

    @Test("the lifeline's stop forces the running VM off")
    func stopForExit() async throws {
        let fixture = try await ServiceFixture()
        try await fixture.supervisor.start(
            generation: 1, cpuCount: 2, memoryMiB: 1024, macAddress: testMAC)
        await fixture.supervisor.stopForExit()
        #expect(await fixture.engine.machines.first?.stops == ["force"])
        #expect(await fixture.supervisor.status.state == .stopped)
    }
}
