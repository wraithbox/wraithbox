// The one file in the package's sources with a NAT network, which
// `.swiftlint.yml` excepts from the static check of S04-02-vm-provider
// ("What only the code keeps out"). It is in the dev-only tool, which is
// never in the app bundle (S10-tech-stack, "Test tooling"), and serves only
// the first boot of a test guest in the spike cache (I350). `wb-vmd` never
// configures a NAT network (SEC05-default-deny).
import Foundation
import Virtualization

/// The VM of a never-booted test bundle, on Apple's NAT network, so the
/// host reaches the guest's SSH server.
@MainActor
func provisioningMachine(bundle: String, record: BundleRecord, mac: VZMACAddress) throws
    -> VZVirtualMachine
{
    VZVirtualMachine(
        configuration: try machineConfiguration(
            bundle: bundle, record: record, network: VZNATNetworkDeviceAttachment(), mac: mac))
}

/// Fails when this build or host can't provision a guest: the API is new
/// in macOS 27.
func checkProvisioningAvailable() throws(Failure) {
    #if compiler(>=6.4)
        guard #available(macOS 27, *) else {
            throw Failure("guest provisioning needs a macOS 27 host")
        }
    #else
        throw Failure("guest provisioning needs a build with Xcode 27 (Swift 6.4) or later")
    #endif
}

/// Starts the VM with guest provisioning: macOS creates `user` with
/// `password` on the first boot and turns on Remote Login. The API is new
/// in macOS 27, so a build with an older SDK, or a run on an older host,
/// refuses to start.
@MainActor
func startProvisioning(_ machine: VZVirtualMachine, user: String, password: String)
    async throws
{
    #if compiler(>=6.4)
        guard #available(macOS 27, *) else {
            throw Failure("guest provisioning needs a macOS 27 host")
        }
        let provisioning = VZMacGuestProvisioningOptions()
        provisioning.fullName = "Wraith Box test guest"
        provisioning.username = user
        provisioning.password = password
        provisioning.logsInAutomatically = false
        provisioning.enablesRemoteLogin = true
        let options = VZMacOSVirtualMachineStartOptions()
        try options.setGuestProvisioning(provisioning)
        try await machine.start(options: options)
    #else
        throw Failure("guest provisioning needs a build with Xcode 27 (Swift 6.4) or later")
    #endif
}
