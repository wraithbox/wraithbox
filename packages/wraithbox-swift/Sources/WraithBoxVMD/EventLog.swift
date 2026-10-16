// wb-vmd's log: one JSON object per line on its inherited log
// descriptor, fd 2, which wb-hostd reads, frames and rate-limits
// (S04-03-self-sandboxing, "Daemon logs"). Every record has the time,
// the level and the event. A refusal also has the rule that refused it
// (S04-08-records-and-bounds, "Host work the guest can cause").
//
// The encoder uses no Foundation formatter: under the profile the
// process can't read the time zone or locale files.
import Darwin
import Synchronization

/// How serious a log record is.
public enum LogLevel: String, Sendable {
    case info
    case warn
    case error
}

/// A value of one field of a log record.
public enum LogValue: Sendable, Equatable {
    case string(String)
    case int(Int64)
    case strings([String])

    public static func uint(_ value: some BinaryInteger) -> LogValue {
        .int(Int64(clamping: value))
    }
}

/// Writes log records as JSON lines.
public struct EventLog: Sendable {
    /// Each string field is cut to this many bytes. That keeps the records
    /// wb-vmd writes short, but doesn't bound a record with many fields.
    /// wb-hostd cuts any line over 4 KiB (S04-08-records-and-bounds, "Logs
    /// and health").
    static let maxFieldBytes = 512

    private let sink: @Sendable (String) -> Void
    private let clock: @Sendable () -> String

    public init(
        sink: @escaping @Sendable (String) -> Void,
        clock: @escaping @Sendable () -> String = EventLog.utcNow
    ) {
        self.sink = sink
        self.clock = clock
    }

    /// A log that writes each record to a descriptor, one whole line per
    /// write call sequence, under a lock.
    public static func descriptor(_ fd: Int32) -> EventLog {
        let lock = Mutex(())
        return EventLog { line in
            lock.withLock { _ in
                writeAll(fd, line + "\n")
            }
        }
    }

    public func info(_ event: String, _ fields: [String: LogValue] = [:]) {
        emit(.info, event, rule: nil, fields)
    }

    /// A refusal: always carries the rule.
    public func refusal(_ event: String, rule: String, _ fields: [String: LogValue] = [:]) {
        emit(.warn, event, rule: rule, fields)
    }

    public func error(_ event: String, rule: String? = nil, _ fields: [String: LogValue] = [:]) {
        emit(.error, event, rule: rule, fields)
    }

    private func emit(
        _ level: LogLevel, _ event: String, rule: String?, _ fields: [String: LogValue]
    ) {
        var pairs: [(String, LogValue)] = [
            ("time", .string(clock())),
            ("level", .string(level.rawValue)),
            ("event", .string(event)),
        ]
        if let rule {
            pairs.append(("rule", .string(rule)))
        }
        for key in fields.keys.sorted() where !["time", "level", "event", "rule"].contains(key) {
            if let value = fields[key] {
                pairs.append((key, value))
            }
        }
        sink(Self.encode(pairs))
    }

    /// One JSON object, keys in the given order.
    static func encode(_ pairs: [(String, LogValue)]) -> String {
        var out = "{"
        for (index, (key, value)) in pairs.enumerated() {
            if index > 0 {
                out += ","
            }
            out += quote(key) + ":" + encode(value)
        }
        return out + "}"
    }

    private static func encode(_ value: LogValue) -> String {
        switch value {
        case .string(let text):
            return quote(cut(text))
        case .int(let number):
            return String(number)
        case .strings(let list):
            return "[" + list.map { quote(cut($0)) }.joined(separator: ",") + "]"
        }
    }

    /// Cuts a string to `maxFieldBytes` bytes of UTF-8. A scalar cut in
    /// half becomes U+FFFD.
    static func cut(_ text: String) -> String {
        guard text.utf8.count > maxFieldBytes else {
            return text
        }
        return String(decoding: text.utf8.prefix(maxFieldBytes), as: UTF8.self)
    }

    /// A JSON string literal. Control characters are escaped, so a value
    /// from a peer or the framework can't start a new log line.
    static func quote(_ text: String) -> String {
        var out = "\""
        for scalar in text.unicodeScalars {
            switch scalar {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\n": out += "\\n"
            case "\r": out += "\\r"
            case "\t": out += "\\t"
            default:
                if scalar.value < 0x20 || scalar.value == 0x7F {
                    out += "\\u" + hex4(scalar.value)
                } else {
                    out.unicodeScalars.append(scalar)
                }
            }
        }
        return out + "\""
    }

    private static func hex4(_ value: UInt32) -> String {
        let digits = String(value, radix: 16)
        return String(repeating: "0", count: max(0, 4 - digits.count)) + digits
    }

    /// The current time in UTC, RFC 3339 with microseconds.
    @Sendable public static func utcNow() -> String {
        var now = timespec()
        clock_gettime(CLOCK_REALTIME, &now)
        var seconds = now.tv_sec
        var parts = tm()
        gmtime_r(&seconds, &parts)
        let date = [
            pad(Int(parts.tm_year) + 1900, 4), "-", pad(Int(parts.tm_mon) + 1, 2), "-",
            pad(Int(parts.tm_mday), 2),
        ]
        let time = [
            pad(Int(parts.tm_hour), 2), ":", pad(Int(parts.tm_min), 2), ":",
            pad(Int(parts.tm_sec), 2), ".", pad(Int(now.tv_nsec / 1000), 6),
        ]
        return date.joined() + "T" + time.joined() + "Z"
    }

    private static func pad(_ value: Int, _ width: Int) -> String {
        let digits = String(value)
        return String(repeating: "0", count: max(0, width - digits.count)) + digits
    }
}

/// Writes all of `text` to `fd`, retrying short writes and EINTR. A
/// failed write is dropped: there is nowhere else to report it.
func writeAll(_ fd: Int32, _ text: String) {
    var bytes = Array(text.utf8)
    var offset = 0
    while offset < bytes.count {
        let written = bytes.withUnsafeMutableBytes { buffer in
            write(fd, buffer.baseAddress! + offset, buffer.count - offset)
        }
        if written > 0 {
            offset += written
        } else if written < 0 && errno == EINTR {
            continue
        } else {
            return
        }
    }
}
