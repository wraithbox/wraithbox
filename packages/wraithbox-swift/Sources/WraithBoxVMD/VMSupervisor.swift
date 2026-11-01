// wb-vmd's one VM and the rules of its requests (S04-04-vm-processes,
// "One `wb-vmd` per VM"). Every refusal names its rule; VMServiceHandler
// logs it and returns it in the gRPC error.
import Darwin
import GRPCCore
import Synchronization
import WraithBoxRPC
import WraithBoxVM

/// A VM's state with its generation, as VMState in the contract.
public struct VMStatus: Sendable, Equatable {
    public var generation: UInt64
    public var state: VMRunState
    public var failure: VMFailure?
}

/// The rules of VMService's refusals, besides the device-set rules that
/// pass through.
public enum VMRule: String, Sendable, CaseIterable {
    /// `cpu_count` or `memory_mib` is 0 or outside the host's range.
    case resources = "vm-resources"
    /// Start while the VM runs, starts or stops.
    case alreadyRunning = "vm-already-running"
    /// Generation 0, a Start for a generation not above the last one, or
    /// a Stop or ConnectGuest for one other than the current one. A Start
    /// that failed used its generation up: the state it published names
    /// it, so the next Start needs a higher one.
    case generation = "vm-generation"
    /// A Stop without a mode.
    case stopMode = "vm-stop-mode"
    /// A Stop while the framework is still starting the VM.
    case starting = "vm-starting"
    /// The framework refused the configuration.
    case config = "vm-config"
    /// The framework failed to start the VM.
    case startFailed = "vm-start-failed"
    /// The framework failed to stop the VM.
    case stopFailed = "vm-stop-failed"
    /// Too many WatchState streams at once.
    case watchLimit = "vm-watch-limit"
    /// A ConnectGuest to a guest port of 1024 or above (S04-02-vm-provider,
    /// "Guest ports below 1024").
    case guestPortNotReserved = "guest-port-not-reserved"
    /// A ConnectGuest with request_id 0, which proto3 gives an absent
    /// field.
    case requestID = "vm-request-id"
    /// A ConnectGuest while the VM isn't running, or after it stopped
    /// during the connect.
    case notRunning = "vm-not-running"
    /// A ConnectGuest while GuestConnector.maxInFlight connects are in
    /// flight (SEC13-bounded-resources).
    case connectCap = "connect-cap"
    /// The framework failed to open the connection, such as when nothing
    /// listens on the guest port.
    case connectFailed = "vm-connect-failed"
    /// The framework didn't open the connection in time.
    case connectTimeout = "vm-connect-timeout"
    /// The hand-off record couldn't be sent.
    case handoffSendFailed = "handoff-send-failed"
    /// A Start whose `mac_address` isn't six bytes of a locally
    /// administered unicast address.
    case macInvalid = "vmd-mac-invalid"
    /// The packet transport's socketpair couldn't be made or set up
    /// (S07-02-packet-path, "Packet transport").
    case networkFailed = "vm-network-failed"
}

/// A refused request.
public struct VMRefusal: Error, Sendable, Equatable {
    /// The rule: a VMRule or a DeviceSetRule.
    public let rule: String
    public let code: RPCError.Code
    public let detail: String

    init(_ rule: VMRule, _ code: RPCError.Code, _ detail: String) {
        self.init(rule: rule.rawValue, code: code, detail: detail)
    }

    init(rule: String, code: RPCError.Code, detail: String) {
        self.rule = rule
        self.code = code
        self.detail = detail
    }
}

/// A WatchState stream: the states, and the call that ends them when the
/// RPC is cancelled.
public struct StateWatch: Sendable {
    public let states: AsyncStream<VMStatus>
    public let end: @Sendable () -> Void
}

/// The WatchState streams that ended, recorded from whichever thread ended
/// them.
final class EndedWatchers: Sendable {
    private let ids = Mutex<[UInt64]>([])

    func add(_ id: UInt64) {
        ids.withLock { $0.append(id) }
    }

    func take() -> [UInt64] {
        ids.withLock { taken in
            defer { taken = [] }
            return taken
        }
    }
}

/// How Stop stops the VM.
public enum StopMode: String, Sendable {
    case request
    case force
}

/// Runs at most one VM, in the slot's bundle.
@MainActor
public final class VMSupervisor {
    /// The most WatchState streams at once. A value in code: wb-hostd
    /// needs one (S04-08-records-and-bounds, "Host work the guest can
    /// cause").
    nonisolated static let maxWatchers = 4

    private let engine: any VMEngine
    private let slot: VMSlot
    private let bundlePath: String
    private let handoff: HandoffChannel
    private let log: EventLog
    private var vm: (any EngineVM)?
    private var starting = false
    private var lastGeneration: UInt64 = 0
    private var watchers: [UInt64: AsyncStream<VMStatus>.Continuation] = [:]
    private var nextWatcher: UInt64 = 0
    private let ended = EndedWatchers()

    /// The current state. Before the first start: generation 0, stopped.
    public private(set) var status = VMStatus(generation: 0, state: .stopped, failure: nil)

    public init(
        engine: any VMEngine, slot: VMSlot, bundlePath: String, handoff: HandoffChannel,
        log: EventLog
    ) {
        self.engine = engine
        self.slot = slot
        self.bundlePath = bundlePath
        self.handoff = handoff
        self.log = log
    }

    /// Starts the VM, after every check. Returns once the framework has
    /// started it. When the slot's set has a network device, wb-hostd
    /// has the host end of its packet transport before the VM starts.
    public func start(
        generation: UInt64, cpuCount: UInt32, memoryMiB: UInt64, macAddress: [UInt8]
    ) async throws(VMRefusal) {
        guard !starting, !status.state.isActive else {
            throw VMRefusal(
                .alreadyRunning, .failedPrecondition, "the VM is \(status.state.rawValue)")
        }
        try checkNewGeneration(generation)
        let mac = try MACAddress(validating: macAddress)
        var request = try bootRequest(cpuCount: cpuCount, memoryMiB: memoryMiB, mac: mac)
        let transport: PacketTransport? = try request.networks > 0 ? PacketTransport() : nil
        // The engine keeps its own copy of the VM end, so wb-vmd's copies
        // are closed on every way out.
        defer { transport?.close() }
        request.networkDescriptor = transport?.vmEnd
        starting = true
        defer { starting = false }
        // Used up even when the start fails (VMRule.generation).
        lastGeneration = generation
        publish(VMStatus(generation: generation, state: .starting, failure: nil))
        let machine = try makeMachine(request, generation: generation)
        if let transport {
            try passTransport(transport, generation: generation, mac: mac)
        }
        vm = machine
        do {
            try await machine.start()
        } catch {
            vm = nil
            publish(VMStatus(generation: generation, state: .error, failure: error))
            throw VMRefusal(.startFailed, .failedPrecondition, error.description)
        }
    }

    /// Builds the VM of `generation`. A configuration the framework
    /// refuses publishes the error state and is refused with `vm-config`.
    private func makeMachine(_ request: BootRequest, generation: UInt64) throws(VMRefusal)
        -> any EngineVM
    {
        do {
            return try engine.makeVM(request) { [weak self] state, failure in
                self?.engineState(state, failure, generation: generation)
            }
        } catch {
            publish(VMStatus(generation: generation, state: .error, failure: error))
            throw VMRefusal(.config, .failedPrecondition, error.description)
        }
    }

    /// Sends the host end of the packet transport to wb-hostd in one
    /// KIND_NETWORK record for `generation`, and closes wb-vmd's copy
    /// (S07-02-packet-path, "Packet transport"; X18-vsock-handoff,
    /// condition 4). A failed send publishes the error state: the VM
    /// doesn't start without the far end of its network device.
    private func passTransport(_ transport: PacketTransport, generation: UInt64, mac: MACAddress)
        throws(VMRefusal)
    {
        defer { transport.closeHostEnd() }
        var message = Wraithbox_Vmd_V1_HandoffMessage()
        message.kind = .network
        message.generation = generation
        var detail = ""
        if let record = try? message.serializedBytes() as [UInt8] {
            if let failed = handoff.send(record, descriptor: transport.hostEnd) {
                detail = "sendmsg: \(String(cString: strerror(failed)))"
            }
        } else {
            detail = "encode failed"
        }
        guard detail.isEmpty else {
            publish(
                VMStatus(
                    generation: generation, state: .error,
                    failure: VMFailure(description: "packet transport: \(detail)")))
            throw VMRefusal(.handoffSendFailed, .failedPrecondition, detail)
        }
        log.info(
            "network-transport-passed",
            [
                "generation": .uint(generation), "mac_address": .string(mac.description),
                "mtu": .int(Int64(PacketTransport.mtu)),
            ])
    }

    /// Stops the VM of `generation`. A mode of nil is the contract's
    /// STOP_MODE_UNSPECIFIED, which is refused.
    public func stop(generation: UInt64, mode: StopMode?) async throws(VMRefusal) {
        try checkCurrentGeneration(generation)
        guard let mode else {
            throw VMRefusal(.stopMode, .invalidArgument, "a stop names its mode")
        }
        // A stop of a VM the framework is still starting fails, or leaves
        // it running, so the caller asks again once it runs.
        guard !starting else {
            throw VMRefusal(.starting, .failedPrecondition, "the VM is starting")
        }
        guard let vm, status.state.isActive else {
            log.info(
                "vm-stop-not-running",
                ["generation": .uint(generation), "mode": .string(mode.rawValue)])
            return
        }
        // The guest was asked already. Only a forced stop changes anything.
        guard mode == .force || status.state != .stopping else {
            log.info("vm-stop-already-stopping", ["generation": .uint(generation)])
            return
        }
        log.info("vm-stop", ["generation": .uint(generation), "mode": .string(mode.rawValue)])
        do {
            switch mode {
            case .request:
                try vm.requestStop()
            case .force:
                try await vm.forceStop()
            }
        } catch {
            throw VMRefusal(.stopFailed, .failedPrecondition, error.description)
        }
    }

    /// The VM of `generation`, for ConnectGuest, when it runs.
    func runningVM(generation: UInt64) throws(VMRefusal) -> any EngineVM {
        try checkCurrentGeneration(generation)
        guard let vm, !starting, status.state == .running else {
            throw VMRefusal(.notRunning, .failedPrecondition, "the VM is \(status.state.rawValue)")
        }
        return vm
    }

    /// A stream of the current state, then each change, and the call
    /// that ends it.
    public func watch() throws(VMRefusal) -> StateWatch {
        // A stream ends on another thread, so its slot is freed here, at
        // once, rather than by a task that may run after the next call.
        for id in ended.take() {
            watchers[id] = nil
        }
        guard watchers.count < Self.maxWatchers else {
            throw VMRefusal(.watchLimit, .resourceExhausted, "\(Self.maxWatchers) streams are open")
        }
        let id = nextWatcher
        nextWatcher += 1
        let (stream, continuation) = AsyncStream.makeStream(
            of: VMStatus.self, bufferingPolicy: .bufferingNewest(16))
        continuation.onTermination = { [ended] _ in
            ended.add(id)
        }
        continuation.yield(status)
        watchers[id] = continuation
        return StateWatch(states: stream, end: { continuation.finish() })
    }

    /// Stops the VM at once for wb-vmd's exit, whatever its generation.
    public func stopForExit() async {
        // A start in progress finishes first, so the stop reaches the VM.
        // wb-vmd's exit deadline bounds the wait.
        while starting {
            try? await Task.sleep(for: .milliseconds(50))
        }
        guard let vm, status.state.isActive else {
            return
        }
        do {
            try await vm.forceStop()
        } catch {
            log.error(
                "vm-stop-failed", rule: VMRule.stopFailed.rawValue,
                ["detail": .string(error.description)])
        }
    }

    private func checkNewGeneration(_ generation: UInt64) throws(VMRefusal) {
        guard generation != 0 else {
            throw VMRefusal(.generation, .invalidArgument, "generation 0")
        }
        guard generation > lastGeneration else {
            throw VMRefusal(
                .generation, .failedPrecondition,
                "generation \(generation) is not above \(lastGeneration)")
        }
    }

    private func checkCurrentGeneration(_ generation: UInt64) throws(VMRefusal) {
        guard generation != 0 else {
            throw VMRefusal(.generation, .invalidArgument, "generation 0")
        }
        guard generation == status.generation else {
            throw VMRefusal(
                .generation, .failedPrecondition,
                "generation \(generation) is not the current \(status.generation)")
        }
    }

    /// Checks the resources and the device set, and logs the devices it
    /// is about to create. Zero is refused, never defaulted: the defaults
    /// are wb-hostd's (S06-06-devices-and-warm-start, "Resources").
    private func bootRequest(cpuCount: UInt32, memoryMiB: UInt64, mac: MACAddress)
        throws(VMRefusal) -> BootRequest
    {
        let limits = engine.limits
        let mib: UInt64 = 1 << 20
        guard limits.cpuCount.contains(Int(cpuCount)) else {
            throw VMRefusal(
                .resources, .invalidArgument, "cpu_count \(cpuCount) is outside \(limits.cpuCount)")
        }
        guard memoryMiB <= UInt64.max / mib, limits.memoryBytes.contains(memoryMiB * mib) else {
            throw VMRefusal(
                .resources, .invalidArgument,
                "memory_mib \(memoryMiB) is outside \(limits.memoryBytes.lowerBound / mib)...\(limits.memoryBytes.upperBound / mib)"
            )
        }
        let description = VMDescription.fixedSet(bundlePath: bundlePath, slot: slot)
        let check = DeviceSetCheck(slot: slot, bundlePath: bundlePath) { [log] line in
            log.info("device-set-check", ["detail": .string(line)])
        }
        do {
            try check.check(description)
        } catch {
            throw VMRefusal(
                rule: error.rule.rawValue, code: .failedPrecondition, detail: error.detail)
        }
        log.info("vm-devices", ["devices": .strings(description.devices.map(\.logName))])
        return BootRequest(
            description: description, bundlePath: bundlePath, cpuCount: Int(cpuCount),
            memoryBytes: memoryMiB * mib, macAddress: mac)
    }

    private func engineState(_ state: VMRunState, _ failure: VMFailure?, generation: UInt64) {
        // A late event from an earlier VM changes nothing.
        guard generation == lastGeneration else {
            return
        }
        publish(
            VMStatus(generation: generation, state: state, failure: state == .error ? failure : nil)
        )
    }

    private func publish(_ next: VMStatus) {
        guard next != status else {
            return
        }
        status = next
        var fields: [String: LogValue] = [
            "generation": .uint(next.generation), "state": .string(next.state.rawValue),
        ]
        if let failure = next.failure {
            fields["detail"] = .string(failure.description)
            fields["failure_reason"] = .string(failure.failureReason)
            fields["code"] = .int(failure.code)
        }
        log.info("vm-state", fields)
        for continuation in watchers.values {
            continuation.yield(next)
        }
    }
}

extension VMDevice {
    /// The device's name in the `vm-devices` log record.
    public var logName: String {
        switch self {
        case .vsock: "vsock"
        case .entropy: "entropy"
        case .disk(let disk): "disk:\(disk.role.rawValue):\(disk.readOnly ? "ro" : "rw")"
        case .macGraphics(let displays): "mac-graphics:\(displays)"
        case .usbKeyboard: "usb-keyboard"
        case .usbScreenPointer: "usb-pointer"
        case .network(let attachment): "network:\(attachment.rawValue)"
        case .directoryShare(let share): "share:\(share.rawValue)"
        case .audioInput: "audio-input"
        case .console(let kind): "console:\(kind.rawValue)"
        case .storage(let kind): "storage:\(kind.rawValue)"
        case .unknown(let name): "unknown:\(name)"
        }
    }
}
