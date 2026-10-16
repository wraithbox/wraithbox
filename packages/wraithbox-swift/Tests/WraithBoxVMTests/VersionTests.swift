import Testing

@testable import WraithBoxVM

@Suite("Version")
struct VersionTests {
    /// `swift test` links no stamp section, so the test bundle is an
    /// unstamped build: it has no version.
    @Test("a bare toolchain build is unstamped")
    func unstamped() {
        #expect(WraithBoxVersion.stampSection() == nil)
        #expect(WraithBoxVersion.version == "")
    }

    @Test(
        "version line names the binary",
        arguments: [
            ("wb-vmd", "wb-vmd (Wraith Box) unstamped"),
            ("other", "other (Wraith Box) unstamped"),
        ])
    func line(binary: String, expected: String) {
        #expect(WraithBoxVersion.line(binary: binary) == expected)
    }

    @Test(
        "a stamp is read only in the version form",
        arguments: [
            ("0.1.0+g0123456789abcdef", "0.1.0+g0123456789abcdef"),
            ("0.1.0+g0123456789abcdef.dirty.00ff", "0.1.0+g0123456789abcdef.dirty.00ff"),
            ("0.1.0+g01\n", ""),
            ("0.0.0-dev ", ""),
            ("", ""),
        ])
    func read(stamp: String, expected: String) {
        #expect(WraithBoxVersion.read(Array(stamp.utf8)) == expected)
    }

    @Test("no stamp section is no version")
    func readNothing() {
        #expect(WraithBoxVersion.read(nil) == "")
    }

    // The same cases as TestValid in the Go package internal/version.
    @Test(
        "the version form",
        arguments: [
            ("1.2.3", true),
            ("0.0.0", true),
            ("10.200.3000", true),
            ("1.2.3-rc.1", true),
            ("1.2.3+g1a2b3c4d", true),
            ("1.2.3+g1a2b3c4d.dirty.0011aabb", true),
            ("1.2.3-beta-2+build.7", true),
            ("1.2.3+a-b-c", true),
            ("", false),
            ("1.2", false),
            ("1.2.3.4", false),
            ("v1.2.3", false),
            ("1..3", false),
            ("1.2.x", false),
            ("1.2.3-", false),
            ("1.2.3+", false),
            ("1.2.3-+b", false),
            ("1.2.3+b+c", false),
            ("1.2.3+b-c+d", false),
            ("1.2.3+b_c", false),
            ("1.2.3+b c", false),
            ("1.2.3+b\u{0}", false),
            ("1.2.3\n", false),
            ("1.2.3+café", false),
            ("0-dev", false),
            ("1.2.3+" + String(repeating: "a", count: 58), true),
            ("1.2.3+" + String(repeating: "a", count: 59), false),
        ])
    func form(version: String, valid: Bool) {
        #expect(WraithBoxVersion.isVersionForm(Array(version.utf8)) == valid)
    }
}
