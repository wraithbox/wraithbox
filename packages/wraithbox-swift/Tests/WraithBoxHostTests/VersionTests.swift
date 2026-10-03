import Testing

@testable import WraithBoxHost

@Suite("Version")
struct VersionTests {
    @Test(
        "version line names the binary",
        arguments: [
            ("wb-hostd", "wb-hostd (Wraith Box) 0.0.0-dev"),
            ("other", "other (Wraith Box) 0.0.0-dev"),
        ])
    func line(binary: String, expected: String) {
        #expect(WraithBoxVersion.line(binary: binary) == expected)
    }
}
