// wb-vmd's main, in the order S04-03-self-sandboxing gives: the bounded
// work before confinement, the profile, the deny probe, then the start
// interface checks, and only then the VM and gRPC. Any refusal is logged
// with its rule, and wb-vmd exits.
import Darwin
import Dispatch
import GRPCCore
import GRPCNIOTransportHTTP2Posix
import NIOPosix
import WraithBoxVM

/// The wb-vmd process.
public enum VMDaemon {
    /// How long wb-vmd waits for its VM to stop when the lifeline closes,
    /// before it exits anyway. Its VM ends with the process.
    static let exitDeadlineSeconds = 10

    /// Runs wb-vmd. Never returns.
    public static func main() -> Never {
        // A closed log pipe or peer must fail a write, not end the process
        // before it stops its VM.
        signal(SIGPIPE, SIG_IGN)
        let log = EventLog.descriptor(InheritedDescriptor.log.rawValue)
        let resolved: ResolvedSlot
        do {
            resolved = try prepare(log: log)
        } catch {
            log.refusal(
                "start-refused", rule: error.rule.rawValue, ["detail": .string(error.detail)])
            exit(1)
        }
        serve(resolved, log: log)
    }

    /// Everything up to the VM. Before `Confinement.apply`, it reads only
    /// the cache directory, WB_VMD_SLOT, the user database and the bundle
    /// path (S04-03-self-sandboxing, "`wb-vmd` before it confines
    /// itself").
    static func prepare(log: EventLog) throws(StartupRefusal) -> ResolvedSlot {
        let cache = try SlotResolution.cacheDirectory()
        let slotValue = getenv(SlotResolution.variable).map { String(cString: $0) }
        let home = SlotResolution.homeDirectory()
        let resolved = try SlotResolution.resolve(slotValue: slotValue, home: home)
        // SEC12-least-privilege: confined to the slot's one bundle.
        try Confinement.apply(bundle: resolved.realBundle, cache: cache)
        try Confinement.probe()
        log.info(
            "confined",
            [
                "slot": .string(resolved.slot.rawValue), "bundle": .string(resolved.realBundle),
                "probe": .string(Confinement.probePath + " denied"),
            ])
        try StartInterface.checkArguments(CommandLine.arguments)
        try StartInterface.checkDescriptors()
        return resolved
    }

    static func serve(_ resolved: ResolvedSlot, log: EventLog) -> Never {
        log.info(
            "started",
            [
                "version": .string(WraithBoxVersion.version),
                "slot": .string(resolved.slot.rawValue),
                "pid": .int(Int64(getpid())),
            ])
        let supervisor = MainActor.assumeIsolated {
            VMSupervisor(
                engine: VirtualizationEngine(), slot: resolved.slot,
                bundlePath: resolved.realBundle, log: log)
        }
        Lifeline.watch(fd: InheritedDescriptor.lifeline.rawValue) { reason in
            stopAndExit(supervisor, log: log, event: "lifeline-closed", reason: reason, status: 0)
        }
        Task {
            var reason = "the server ended"
            do {
                try await serveGRPC(
                    descriptor: InheritedDescriptor.grpc.rawValue,
                    version: WraithBoxVersion.version,
                    supervisor: supervisor, log: log)
            } catch {
                reason = "\(error)"
            }
            stopAndExit(supervisor, log: log, event: "grpc-server-ended", reason: reason, status: 1)
        }
        dispatchMain()
    }

    /// Serves VMService on a listening socket until the server ends.
    public static func serveGRPC(
        descriptor: Int32, version: String, supervisor: VMSupervisor, log: EventLog,
        handshake: HostHandshake? = nil
    ) async throws {
        let group = MultiThreadedEventLoopGroup(numberOfThreads: 1)
        let listener = InheritedListener(
            eventLoopGroup: group, descriptor: descriptor,
            handshake: handshake ?? HostHandshake(ownVersion: version, log: log))
        let transport = HTTP2ServerTransport.Custom(
            listenerFactory: listener, config: ServerLimits.config)
        let server = GRPCServer(
            transport: transport, services: [VMServiceHandler(supervisor: supervisor, log: log)])
        try await server.serve()
    }

    /// Stops the VM at once and exits, within the deadline.
    private static func stopAndExit(
        _ supervisor: VMSupervisor, log: EventLog, event: String, reason: String, status: Int32
    ) {
        DispatchQueue.global().asyncAfter(deadline: .now() + .seconds(exitDeadlineSeconds)) {
            log.error("exit-deadline", ["event": .string(event)])
            exit(status)
        }
        Task { @MainActor in
            await supervisor.stopForExit()
            log.info(event, ["reason": .string(reason)])
            exit(status)
        }
    }
}
