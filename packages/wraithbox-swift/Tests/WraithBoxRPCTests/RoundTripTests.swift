import SwiftProtobuf
import Testing
import WraithBoxRPC

// Round trips every message of wraithbox.vmd.v1 through its protobuf
// encoding, with every field set, so a generated type that drops or
// changes a field fails (B52-proto-contracts). The Go test in
// internal/contracttest also checks that it covers every message.

private func roundTrip<M: SwiftProtobuf.Message & Equatable>(_ message: M) throws -> M {
    let bytes: [UInt8] = try message.serializedBytes()
    return try M(serializedBytes: bytes)
}

private func vmError() -> Wraithbox_Vmd_V1_VMError {
    var error = Wraithbox_Vmd_V1_VMError()
    error.code = 12
    error.failureReason = "Permission denied"
    error.errno = 13
    error.description_p = "The virtual machine failed to restore."
    return error
}

private func vmState() -> Wraithbox_Vmd_V1_VMState {
    var state = Wraithbox_Vmd_V1_VMState()
    state.generation = 7
    state.state = .error
    state.error = vmError()
    return state
}

@Test func startRoundTrips() throws {
    var request = Wraithbox_Vmd_V1_StartRequest()
    request.generation = 1
    request.cpuCount = 4
    request.memoryMib = 8192
    #expect(try roundTrip(request) == request)
    #expect(try roundTrip(Wraithbox_Vmd_V1_StartResponse()) == Wraithbox_Vmd_V1_StartResponse())
}

@Test func stopRoundTrips() throws {
    var request = Wraithbox_Vmd_V1_StopRequest()
    request.generation = 2
    request.mode = .force
    #expect(try roundTrip(request) == request)
    #expect(try roundTrip(Wraithbox_Vmd_V1_StopResponse()) == Wraithbox_Vmd_V1_StopResponse())
}

@Test func stateRoundTrips() throws {
    #expect(try roundTrip(vmError()) == vmError())
    #expect(try roundTrip(vmState()) == vmState())
    #expect(try roundTrip(Wraithbox_Vmd_V1_GetStateRequest()) == Wraithbox_Vmd_V1_GetStateRequest())
    #expect(
        try roundTrip(Wraithbox_Vmd_V1_WatchStateRequest()) == Wraithbox_Vmd_V1_WatchStateRequest())

    var get = Wraithbox_Vmd_V1_GetStateResponse()
    get.state = vmState()
    let gotGet = try roundTrip(get)
    #expect(gotGet == get)
    #expect(gotGet.state.error.failureReason == "Permission denied")

    var watch = Wraithbox_Vmd_V1_WatchStateResponse()
    watch.state = vmState()
    #expect(try roundTrip(watch) == watch)
}

@Test func connectGuestRoundTrips() throws {
    var request = Wraithbox_Vmd_V1_ConnectGuestRequest()
    request.generation = 3
    request.requestID = 41
    request.guestPort = 700
    #expect(try roundTrip(request) == request)
    #expect(
        try roundTrip(Wraithbox_Vmd_V1_ConnectGuestResponse())
            == Wraithbox_Vmd_V1_ConnectGuestResponse())
}

@Test func handoffMessageRoundTrips() throws {
    var message = Wraithbox_Vmd_V1_HandoffMessage()
    message.requestID = 41
    message.generation = 3
    message.direction = .hostToGuest
    message.sourcePort = 1_000_000
    message.destinationPort = 700
    let got = try roundTrip(message)
    #expect(got == message)
    #expect(got.direction == .hostToGuest)
}
