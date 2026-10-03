import Testing

@testable import WraithBoxVM

@Suite("Version")
struct VersionTests {
    @Test(
        "version line names the binary",
        arguments: [
            ("wb-vmd", "wb-vmd (Wraith Box) 0.0.0-dev"),
            ("other", "other (Wraith Box) 0.0.0-dev"),
        ])
    func line(binary: String, expected: String) {
        #expect(WraithBoxVersion.line(binary: binary) == expected)
    }
}
