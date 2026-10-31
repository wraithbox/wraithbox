// `provision` (I350, decision of 2026-10-18): the first boot of a test
// bundle with guest provisioning, as the X18-vsock-handoff host tool did.
// The guest gets an SSH user with Remote Login on Apple's NAT network.
// Only test guests in the spike cache get this. Product guests never do.
import Foundation
import Virtualization
import WraithBoxVM

/// The SSH user every provisioned test guest gets.
let sshUser = "wbdev"

/// The file in the bundle with the SSH user's password, mode 0600.
let passwordFileName = "ssh-password"

/// The default and the largest time limit of a provisioning boot.
let defaultMinutes = 60
let maximumMinutes = 24 * 60

@MainActor
func provisionBundle(_ bundle: CachePath, minutes: Int, log: Log) async throws {
    // Before the lock and the record, so a refused host leaves no lock
    // file and the bundle can still be provisioned on a newer host. The
    // literal is HostRequirement.minimumMacOS, because `#available` takes
    // no constant.
    guard #available(macOS 27, *) else {
        throw Refusal(
            .hostUnsupported, bundle.path,
            "guest provisioning needs macOS \(HostRequirement.minimumMacOS) or later on the host")
    }
    // Checked before the lock, so a refused bundle gets no lock file, and
    // again under it, in case another process booted it in between.
    _ = try neverBooted(bundle)
    let lock = try BundleLock.take(bundle: bundle.path)
    var record = try neverBooted(bundle)
    let password = randomPassword()
    let passwordFile = try writePassword(password, bundle: bundle.path)
    record.bootedAt = Date()
    record.sshUser = sshUser
    try record.write(bundle: bundle.path)

    let mac = VZMACAddress.randomLocallyAdministered()
    let machine = try provisioningMachine(bundle: bundle.path, record: record, mac: mac)
    let stop = StopRequest()
    log.step("starting \(bundle.path) with guest provisioning on the NAT network")
    try await startProvisioning(machine, user: sshUser, password: password)
    log.step("started: SSH user \(sshUser), password in \(passwordFile)")
    log.step(
        "the guest's address is the lease for hw_address=1,\(leaseAddress(mac.string))"
            + " in /var/db/dhcpd_leases")
    log.step("runs until the guest shuts down, mise run vm:stop, or \(minutes) minutes pass")
    let ended = await waitForEnd(
        machine, stop: stop, deadline: ContinuousClock.now + .seconds(minutes * 60))
    log.step("ending: \(ended)")
    await shutDown(machine, log: log)
    withExtendedLifetime(lock) {}
}

/// The record of a bundle `install` finished and nothing booted yet.
/// Guest provisioning is for the first boot. In the needs-vm check of
/// PR343, a start with provisioning from a bundle that had likely booted
/// before failed with "Operation not supported".
private func neverBooted(_ bundle: CachePath) throws -> BundleRecord {
    let record = try BundleRecord.read(bundle: bundle.path)
    if let booted = record.bootedAt {
        throw Refusal(
            .alreadyBooted, bundle.path,
            "it booted at \(booted.formatted(.iso8601)); clone the installed bundle again")
    }
    return record
}

/// 24 hexadecimal digits from the system's random number generator.
func randomPassword() -> String {
    var generator = SystemRandomNumberGenerator()
    return (0..<3).map { _ in
        let value = generator.next() as UInt32
        return String(value, radix: 16).leftPadded(to: 8)
    }.joined()
}

/// A MAC address as `bootpd` writes it in its leases: each octet without
/// its leading zero.
func leaseAddress(_ mac: String) -> String {
    mac.split(separator: ":", omittingEmptySubsequences: false).map { octet in
        let trimmed = octet.drop { $0 == "0" }
        return trimmed.isEmpty ? "0" : String(trimmed)
    }.joined(separator: ":")
}

/// Writes the password into the bundle, readable only by this user, and
/// returns the file's path.
private func writePassword(_ password: String, bundle: String) throws -> String {
    let path = bundle + "/" + passwordFileName
    let fd = open(path, O_WRONLY | O_CREAT | O_TRUNC | O_NOFOLLOW | O_CLOEXEC, 0o600)
    guard fd >= 0 else {
        throw Failure.posix("open", path)
    }
    defer { close(fd) }
    let text = Array((password + "\n").utf8)
    guard fchmod(fd, 0o600) == 0, write(fd, text, text.count) == text.count else {
        throw Failure.posix("write", path)
    }
    return path
}

extension String {
    fileprivate func leftPadded(to width: Int) -> String {
        String(repeating: "0", count: max(0, width - count)) + self
    }
}
