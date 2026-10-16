import MachO

/// Build version shared by the Swift side of Wraith Box
/// (S10-02-packaging-and-signing, "One build version").
public enum WraithBoxVersion {
    /// The Mach-O section that holds the stamp: `mise run swift:build`
    /// has the linker copy `build/version` into it, the file
    /// `mise run version:stamp` writes for the whole build.
    static let segment = "__TEXT"
    static let section = "__wb_version"

    /// The most bytes a version has (S04-08-records-and-bounds, "The
    /// guest's version is a label").
    public static let maxLength = 64

    /// This program's build version, or "" when it is unstamped. A bare
    /// `swift build` or `swift test` links no stamp section, and a stamp
    /// outside the version form counts as none. "" is no version: every
    /// peer refuses it, an unstamped peer included
    /// (S04-08-records-and-bounds, "Version skew").
    public static let version = read(stampSection())

    /// The version line every binary prints for `--version`.
    public static func line(binary: String) -> String {
        "\(binary) (Wraith Box) \(version.isEmpty ? "unstamped" : version)"
    }

    /// The stamp section's bytes in the image this code is linked into,
    /// or nil when it has none. `#dsohandle` is that image's Mach-O
    /// header: the program's own, since the library links statically.
    static func stampSection() -> [UInt8]? {
        let header = #dsohandle.assumingMemoryBound(to: mach_header_64.self)
        var size: UInt = 0
        guard let data = getsectiondata(header, segment, section, &size) else {
            return nil
        }
        return Array(UnsafeBufferPointer(start: data, count: Int(size)))
    }

    /// The version in a stamp, or "" when there is none or it isn't in
    /// the version form.
    static func read(_ stamp: [UInt8]?) -> String {
        guard let stamp, isVersionForm(stamp) else {
            return ""
        }
        return String(decoding: stamp, as: UTF8.self)
    }

    /// Whether the bytes are in the version form of
    /// S04-08-records-and-bounds, "The guest's version is a label":
    /// printable ASCII, 1 to `maxLength` bytes, MAJOR.MINOR.PATCH with an
    /// optional `-` prerelease part and an optional `+` build part, each
    /// of letters, digits, dots and hyphens. The Go side's
    /// `version.Valid` is the same check.
    public static func isVersionForm(_ bytes: [UInt8]) -> Bool {
        guard !bytes.isEmpty, bytes.count <= maxLength,
            var index = numbersEnd(bytes)
        else {
            return false
        }
        for separator in [UInt8(ascii: "-"), UInt8(ascii: "+")]
        where index < bytes.count && bytes[index] == separator {
            let start = index + 1
            index = start
            while index < bytes.count, isPartByte(bytes[index]) {
                index += 1
            }
            guard index > start else {
                return false
            }
        }
        return index == bytes.count
    }

    /// Where MAJOR.MINOR.PATCH, each one or more ASCII digits, ends at the
    /// start of the bytes, or nil when they don't start with it.
    static func numbersEnd(_ bytes: [UInt8]) -> Int? {
        var index = 0
        for number in 0..<3 {
            if number > 0 {
                guard index < bytes.count, bytes[index] == UInt8(ascii: ".") else {
                    return nil
                }
                index += 1
            }
            let start = index
            while index < bytes.count, isDigit(bytes[index]) {
                index += 1
            }
            guard index > start else {
                return nil
            }
        }
        return index
    }

    static func isDigit(_ byte: UInt8) -> Bool {
        byte >= UInt8(ascii: "0") && byte <= UInt8(ascii: "9")
    }

    /// Whether the byte may be in a prerelease or build part. A `+` may
    /// not: it starts the build part and ends the prerelease part.
    static func isPartByte(_ byte: UInt8) -> Bool {
        isDigit(byte) || (byte >= UInt8(ascii: "a") && byte <= UInt8(ascii: "z"))
            || (byte >= UInt8(ascii: "A") && byte <= UInt8(ascii: "Z"))
            || byte == UInt8(ascii: ".") || byte == UInt8(ascii: "-")
    }
}
