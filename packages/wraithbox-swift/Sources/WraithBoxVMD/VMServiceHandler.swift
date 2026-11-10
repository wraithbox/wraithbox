// VMService (proto/wraithbox/vmd/v1/vmd.proto) on top of VMSupervisor.
// A refusal is logged with its rule and returned with status
// FAILED_PRECONDITION or INVALID_ARGUMENT and a google.rpc.ErrorInfo
// detail whose reason is the rule (S04-architecture, "Inter-process
// contracts").
import GRPCCore
import GRPCProtobuf
import WraithBoxRPC

/// Serves VMService for one VM.
public struct VMServiceHandler: Wraithbox_Vmd_V1_VMService.SimpleServiceProtocol {
    /// The ErrorInfo domain of wb-vmd's refusals.
    public static let errorDomain = "wb-vmd"

    let supervisor: VMSupervisor
    let connector: GuestConnector
    let log: EventLog

    public init(supervisor: VMSupervisor, connector: GuestConnector, log: EventLog) {
        self.supervisor = supervisor
        self.connector = connector
        self.log = log
    }

    public func start(request: Wraithbox_Vmd_V1_StartRequest, context: ServerContext) async throws
        -> Wraithbox_Vmd_V1_StartResponse
    {
        log.info(
            "rpc-start",
            [
                "generation": .uint(request.generation), "cpu_count": .uint(request.cpuCount),
                "memory_mib": .uint(request.memoryMib),
                "provisioning": .string(String(request.provisioning)),
            ])
        try await refusing("Start", generation: request.generation) { () async throws(VMRefusal) in
            try await supervisor.start(
                generation: request.generation, cpuCount: request.cpuCount,
                memoryMiB: request.memoryMib, macAddress: Array(request.macAddress),
                provisioning: request.provisioning)
        }
        return Wraithbox_Vmd_V1_StartResponse()
    }

    public func install(
        request: Wraithbox_Vmd_V1_InstallRequest,
        response: RPCWriter<Wraithbox_Vmd_V1_InstallResponse>, context: ServerContext
    ) async throws {
        log.info(
            "rpc-install",
            ["cpu_count": .uint(request.cpuCount), "memory_mib": .uint(request.memoryMib)])
        // The supervisor reports on the main actor, and the stream writes
        // here. At most 101 percents, so the buffer holds them all.
        let (percents, sink) = AsyncStream.makeStream(of: UInt32.self)
        let run = Task { () -> VMRefusal? in
            defer { sink.finish() }
            do throws(VMRefusal) {
                try await supervisor.install(
                    cpuCount: request.cpuCount, memoryMiB: request.memoryMib
                ) { percent in
                    sink.yield(percent)
                }
                return nil
            } catch {
                return error
            }
        }
        for await percent in percents {
            var message = Wraithbox_Vmd_V1_InstallResponse()
            message.percent = percent
            try await response.write(message)
        }
        guard let refusal = await run.value else {
            return
        }
        // A framework failure ends the stream with its VMError, as a
        // VMState carries one, before the call fails. A caller that is gone
        // misses it, and the refusal is still logged.
        if let failure = refusal.failure {
            var message = Wraithbox_Vmd_V1_InstallResponse()
            message.error = Wraithbox_Vmd_V1_VMError(failure)
            try? await response.write(message)
        }
        try await refusing("Install", generation: nil) { () throws(VMRefusal) in
            throw refusal
        }
    }

    public func stop(request: Wraithbox_Vmd_V1_StopRequest, context: ServerContext) async throws
        -> Wraithbox_Vmd_V1_StopResponse
    {
        let mode: StopMode? =
            switch request.mode {
            case .request: .request
            case .force: .force
            case .unspecified, .UNRECOGNIZED: nil
            }
        try await refusing("Stop", generation: request.generation) { () async throws(VMRefusal) in
            try await supervisor.stop(generation: request.generation, mode: mode)
        }
        return Wraithbox_Vmd_V1_StopResponse()
    }

    public func getState(request: Wraithbox_Vmd_V1_GetStateRequest, context: ServerContext)
        async throws
        -> Wraithbox_Vmd_V1_GetStateResponse
    {
        var response = Wraithbox_Vmd_V1_GetStateResponse()
        response.state = await Wraithbox_Vmd_V1_VMState(supervisor.status)
        return response
    }

    public func watchState(
        request: Wraithbox_Vmd_V1_WatchStateRequest,
        response: RPCWriter<Wraithbox_Vmd_V1_WatchStateResponse>, context: ServerContext
    ) async throws {
        var watch: StateWatch?
        try await refusing("WatchState", generation: nil) { () async throws(VMRefusal) in
            watch = try await supervisor.watch()
        }
        guard let watch else {
            return
        }
        // The stream ends when the caller cancels the RPC, so a watcher
        // that went away doesn't hold a slot.
        try await withRPCCancellationHandler {
            for await status in watch.states {
                var message = Wraithbox_Vmd_V1_WatchStateResponse()
                message.state = Wraithbox_Vmd_V1_VMState(status)
                try await response.write(message)
            }
        } onCancelRPC: {
            watch.end()
        }
    }

    public func connectGuest(request: Wraithbox_Vmd_V1_ConnectGuestRequest, context: ServerContext)
        async throws -> Wraithbox_Vmd_V1_ConnectGuestResponse
    {
        log.info(
            "rpc-connect-guest",
            [
                "generation": .uint(request.generation), "request_id": .uint(request.requestID),
                "guest_port": .uint(request.guestPort),
            ])
        // The answer comes after the record is sent and wb-vmd's copy of
        // the descriptor is closed (S04-02-vm-provider, "Hand-off on macOS").
        try await refusing("ConnectGuest", generation: request.generation) {
            () async throws(VMRefusal) in
            try await connector.connect(
                generation: request.generation, requestID: request.requestID,
                port: request.guestPort)
        }
        return Wraithbox_Vmd_V1_ConnectGuestResponse()
    }

    /// Runs `body`, and turns a refusal into a logged gRPC error with
    /// the rule as its ErrorInfo reason.
    private func refusing(
        _ method: String, generation: UInt64?, _ body: () async throws(VMRefusal) -> Void
    ) async throws {
        do {
            try await body()
        } catch {
            var fields: [String: LogValue] = [
                "method": .string(method), "detail": .string(error.detail),
            ]
            if let generation {
                fields["generation"] = .uint(generation)
            }
            log.refusal("rpc-refused", rule: error.rule, fields)
            throw GoogleRPCStatus(
                code: error.code, message: "\(error.rule): \(error.detail)",
                details: [.errorInfo(reason: error.rule, domain: Self.errorDomain)])
        }
    }
}

extension Wraithbox_Vmd_V1_VMState {
    init(_ status: VMStatus) {
        self.init()
        generation = status.generation
        state = Wraithbox_Vmd_V1_State(status.state)
        if let failure = status.failure {
            error = Wraithbox_Vmd_V1_VMError(failure)
        }
    }
}

extension Wraithbox_Vmd_V1_VMError {
    init(_ failure: VMFailure) {
        self.init()
        code = failure.code
        failureReason = failure.failureReason
        errno = failure.errno
        description_p = failure.description
    }
}

extension Wraithbox_Vmd_V1_State {
    init(_ state: VMRunState) {
        switch state {
        case .stopped: self = .stopped
        case .starting: self = .starting
        case .running: self = .running
        case .paused: self = .paused
        case .error: self = .error
        case .pausing: self = .pausing
        case .resuming: self = .resuming
        case .stopping: self = .stopping
        case .saving: self = .saving
        case .restoring: self = .restoring
        }
    }
}
