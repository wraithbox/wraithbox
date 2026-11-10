// C11-host-daemons, "Start interface": the reader of the build password
// pipe, descriptor 5 of the build slot. It reads once, to end of file,
// within a deadline, and refuses an empty, oversized, non-ASCII or late
// password without its bytes in the refusal (SEC04-no-guest-secrets).
import Darwin
import Testing

@testable import WraithBoxVMD

@Suite("Provisioning input")
@MainActor
struct ProvisioningInputTests {
    /// A reader of a pipe that holds `bytes` and is closed at the write end.
    private func reader(_ bytes: [UInt8]) throws -> ProvisioningInput {
        let (readEnd, writeEnd) = try makePipe()
        bytes.withUnsafeBytes { raw in
            _ = Darwin.write(writeEnd, raw.baseAddress, raw.count)
        }
        close(writeEnd)
        return ProvisioningInput(fd: readEnd)
    }

    @Test("a printable ASCII password is read to end of file")
    func reads() throws {
        let input = try reader(Array("correct horse ~1".utf8))
        #expect(try input.read() == "correct horse ~1")
    }

    @Test("a password of exactly maxBytes is read")
    func longest() throws {
        let password = String(repeating: "p", count: ProvisioningInput.maxBytes)
        #expect(try reader(Array(password.utf8)).read() == password)
    }

    @Test(
        "an empty, oversized or non-ASCII password is refused with vmd-provisioning-password-invalid",
        arguments: [
            [UInt8](),
            [UInt8](repeating: 0x73, count: ProvisioningInput.maxBytes + 1),
            Array("hunter2\n".utf8),
            Array("hunter\u{E9}".utf8),
            [0x73, 0x00, 0x74],
        ])
    func refused(bytes: [UInt8]) throws {
        let input = try reader(bytes)
        do {
            _ = try input.read()
            Issue.record("the password was not refused")
        } catch {
            #expect(error.rule == "vmd-provisioning-password-invalid")
            #expect(!error.detail.contains("hunt"))
            #expect(!error.detail.contains("sss"))
        }
    }

    @Test("a password whose writer doesn't close the pipe is refused at the deadline")
    func late() throws {
        let (readEnd, writeEnd) = try makePipe()
        defer { close(writeEnd) }
        writeAll(writeEnd, "hunter2")
        let input = ProvisioningInput(fd: readEnd)
        do {
            _ = try input.read(deadline: .milliseconds(200))
            Issue.record("the password was not refused")
        } catch {
            #expect(error.rule == "vmd-provisioning-password-invalid")
            #expect(error.detail == "the password did not end within the deadline")
        }
    }

    @Test("the password is read once, and the pipe is closed after the read")
    func once() throws {
        let (readEnd, writeEnd) = try makePipe()
        writeAll(writeEnd, "hunter2")
        close(writeEnd)
        let input = ProvisioningInput(fd: readEnd)
        #expect(try input.read() == "hunter2")
        #expect(fcntl(readEnd, F_GETFD) == -1)
        do {
            _ = try input.read()
            Issue.record("the second read was not refused")
        } catch {
            #expect(error.rule == "vmd-provisioning-password-invalid")
            #expect(error.detail == "the password was read already")
        }
    }
}
