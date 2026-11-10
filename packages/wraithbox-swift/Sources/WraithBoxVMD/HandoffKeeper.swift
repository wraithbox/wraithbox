// Keep-until-held for wb-vmd's hand-offs (S07-05-stream-hand-off,
// "Transport"; S04-02-vm-provider, "Hand-off on macOS"). On macOS 27 the
// kernel can collect a socket in flight that no process holds, and it
// then arrives dead (I637). So wb-vmd keeps its copy of each descriptor
// it sends until wb-hostd's HandoffAck for the record arrives, or until
// the exchange's deadline passes, and only then closes it.
//
// A HandoffAck comes from wb-hostd, but it is still checked as input: a
// record of the wrong form, or one for a descriptor wb-vmd doesn't keep,
// is refused and logged with its rule, and the wait it might have ended
// goes on to the deadline. wb-vmd never reads or writes the descriptor it
// keeps (SEC12-least-privilege).
import Dispatch
import SwiftProtobuf
import WraithBoxRPC

/// The rules of the hand-off acknowledgments.
public enum HandoffAckRule: String, Sendable, CaseIterable {
    /// A record that is empty, over 256 bytes, truncated, carries a
    /// descriptor, doesn't decode as a HandoffAck, or names a kind,
    /// request ID or generation no hand-off record can have.
    case malformed = "handoff-ack-malformed"
    /// A HandoffAck for no descriptor wb-vmd keeps.
    case unexpected = "handoff-ack-unexpected"
    /// No HandoffAck came for a kept descriptor within the deadline. It
    /// is closed all the same.
    case timeout = "handoff-ack-timeout"
    /// The channel can't be read any more. Every kept descriptor is
    /// closed at its deadline.
    case channelEnded = "handoff-ack-channel-ended"
}

/// Sends hand-off records and keeps each record's descriptor until
/// wb-hostd acknowledges it, or until the deadline.
@MainActor
public final class HandoffKeeper {
    /// How long wb-vmd keeps a sent descriptor without an acknowledgment:
    /// the exchange's deadline, the connect timeout (S04-02-vm-provider,
    /// "Hand-off on macOS"). A value in code.
    public nonisolated static let defaultDeadline = Duration.seconds(5)

    /// What an acknowledgment names: the kind, request ID and generation
    /// of the record it answers.
    struct Key: Hashable {
        var kind: Wraithbox_Vmd_V1_Kind
        var requestID: UInt64
        var generation: UInt64
    }

    /// One kept descriptor: how to close it, and its deadline.
    private struct Kept {
        let token: UInt64
        let close: @MainActor () -> Void
        let timer: Task<Void, Never>
    }

    private let channel: HandoffChannel
    private let log: EventLog
    private let deadline: Duration
    private let reader: any DispatchSourceRead
    /// The kept descriptors by key, oldest first: wb-hostd answers the
    /// records in the order they came, so an acknowledgment closes the
    /// oldest of its key.
    private var kept: [Key: [Kept]] = [:]
    private var nextToken: UInt64 = 0

    /// How many descriptors wb-vmd keeps now.
    public var keptCount: Int {
        kept.values.reduce(0) { $0 + $1.count }
    }

    /// Makes the keeper of `channel`, and starts reading acknowledgments
    /// on the main queue.
    public init(
        channel: HandoffChannel, log: EventLog, deadline: Duration = HandoffKeeper.defaultDeadline
    ) {
        self.channel = channel
        self.log = log
        self.deadline = deadline
        reader = DispatchSource.makeReadSource(fileDescriptor: channel.fd, queue: .main)
        reader.setEventHandler { [weak self] in
            MainActor.assumeIsolated { self?.drain() }
        }
        reader.resume()
    }

    deinit {
        reader.cancel()
    }

    /// Sends `message` with `descriptor`. On success the keeper owns the
    /// descriptor: it calls `close` once, when the acknowledgment comes or
    /// the deadline passes. On failure it returns the errno, or EINVAL
    /// when the message doesn't encode, and the caller still owns the
    /// descriptor.
    public func send(
        _ message: Wraithbox_Vmd_V1_HandoffMessage, descriptor: Int32,
        close: @escaping @MainActor () -> Void
    ) -> Int32? {
        guard let record = try? message.serializedBytes() as [UInt8] else {
            return EINVAL
        }
        if let failed = channel.send(record, descriptor: descriptor) {
            return failed
        }
        keep(
            Key(kind: message.kind, requestID: message.requestID, generation: message.generation),
            close: close)
        return nil
    }

    private func keep(_ key: Key, close: @escaping @MainActor () -> Void) {
        let token = nextToken
        nextToken += 1
        let timer = Task { [weak self, deadline] in
            guard (try? await Task.sleep(for: deadline)) != nil else {
                return
            }
            self?.expire(key, token: token)
        }
        kept[key, default: []].append(Kept(token: token, close: close, timer: timer))
    }

    /// Closes the kept descriptor `token` of `key` at its deadline.
    private func expire(_ key: Key, token: UInt64) {
        guard let index = kept[key]?.firstIndex(where: { $0.token == token }) else {
            return
        }
        let entry = kept[key]?.remove(at: index)
        if kept[key]?.isEmpty == true {
            kept[key] = nil
        }
        entry?.close()
        log.refusal(
            "handoff-ack-timeout", rule: HandoffAckRule.timeout.rawValue,
            Self.fields(key).merging(["deadline": .string("\(deadline)")]) { $1 })
    }

    /// Reads every queued record.
    private func drain() {
        var read = 0
        while true {
            switch channel.receive() {
            case .none:
                if read == 0, channel.peerClosed() {
                    // A wake with nothing to read, from a peer that is
                    // gone: stop, rather than wake again and again.
                    end("wb-hostd closed its end")
                }
                return
            case .record(let record):
                acknowledge(record)
            case .refused(let detail):
                log.refusal(
                    "handoff-ack-refused", rule: HandoffAckRule.malformed.rawValue,
                    ["detail": .string(detail)])
            case .ended(let detail):
                end(detail)
                return
            }
            read += 1
        }
    }

    /// Stops reading the channel. Every kept descriptor is closed at its
    /// deadline.
    private func end(_ detail: String) {
        reader.cancel()
        log.error(
            "handoff-ack-channel-ended", rule: HandoffAckRule.channelEnded.rawValue,
            ["detail": .string(detail), "kept": .int(Int64(keptCount))])
    }

    /// Checks one record from wb-hostd and, when it acknowledges a kept
    /// descriptor, closes the oldest one of its key.
    func acknowledge(_ record: [UInt8]) {
        let key: Key
        let accepted: Bool
        switch Self.check(record) {
        case .failure(let refusal):
            log.refusal(
                "handoff-ack-refused", rule: refusal.rule.rawValue,
                ["detail": .string(refusal.detail), "bytes": .int(Int64(record.count))])
            return
        case .success(let ack):
            key = Key(kind: ack.kind, requestID: ack.requestID, generation: ack.generation)
            accepted = ack.accepted
        }
        var fields = Self.fields(key)
        // What wb-hostd decided about the record, for the log only.
        fields["host_decision"] = .string(accepted ? "accept" : "refuse")
        guard var entries = kept[key], !entries.isEmpty else {
            log.refusal(
                "handoff-ack-refused", rule: HandoffAckRule.unexpected.rawValue, fields)
            return
        }
        let entry = entries.removeFirst()
        kept[key] = entries.isEmpty ? nil : entries
        entry.timer.cancel()
        entry.close()
        log.info("handoff-acknowledged", fields)
    }

    /// A refused acknowledgment: the rule and why.
    struct AckRefusal: Error, Equatable {
        let rule: HandoffAckRule
        let detail: String
    }

    /// Decodes a record and checks its form, in the order of the comment
    /// on HandoffAck in proto/wraithbox/vmd/v1.
    static func check(_ record: [UInt8]) -> Result<Wraithbox_Vmd_V1_HandoffAck, AckRefusal> {
        func malformed(_ detail: String) -> Result<Wraithbox_Vmd_V1_HandoffAck, AckRefusal> {
            .failure(AckRefusal(rule: .malformed, detail: detail))
        }
        guard record.count <= HandoffChannel.maxRecord else {
            return malformed("record of \(record.count) bytes")
        }
        guard let ack = try? Wraithbox_Vmd_V1_HandoffAck(serializedBytes: record) else {
            return malformed("record of \(record.count) bytes doesn't decode")
        }
        guard ack.unknownFields.data.isEmpty else {
            return malformed(
                "\(ack.unknownFields.data.count) bytes of fields HandoffAck doesn't have")
        }
        guard ack.generation != 0 else {
            return malformed("generation 0")
        }
        switch ack.kind {
        case .connection where ack.requestID == 0:
            return malformed("a connection with request_id 0")
        case .network where ack.requestID != 0:
            return malformed("a packet transport with request_id \(ack.requestID)")
        case .connection, .network:
            return .success(ack)
        default:
            return malformed("kind \(ack.kind.rawValue)")
        }
    }

    private static func fields(_ key: Key) -> [String: LogValue] {
        [
            "kind": .string(key.kind == .network ? "network" : "connection"),
            "request_id": .uint(key.requestID), "generation": .uint(key.generation),
        ]
    }
}
