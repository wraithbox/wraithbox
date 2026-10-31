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

/// Starts the VM with guest provisioning: macOS creates `user` with
/// `password` on the first boot and turns on Remote Login. The API is new
/// in macOS 27, which the package doesn't target (HostRequirement).
@available(macOS 27, *)
@MainActor
func startProvisioning(_ machine: VZVirtualMachine, user: String, password: String)
    async throws
{
    let provisioning = VZMacGuestProvisioningOptions()
    provisioning.fullName = "Wraith Box test guest"
    provisioning.username = user
    provisioning.password = password
    provisioning.logsInAutomatically = false
    provisioning.enablesRemoteLogin = true
    let options = VZMacOSVirtualMachineStartOptions()
    try options.setGuestProvisioning(provisioning)
    try await machine.start(options: options)
}
