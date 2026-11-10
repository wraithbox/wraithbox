// The provisioning-options check. Only the `build` slot of
// wb-vmd-install accepts the macOS provisioning options, which create
// `wbadmin` with a password. Every other slot refuses them, the `layer`
// slot of wb-vmd-layer included (S04-10-vmd-install, "The build bundle"
// and S04-04-vm-processes, "`wb-vmd-layer`"; S15-04-guest-admin,
// "Refused outside the path"; SEC12-least-privilege).
import WraithBoxVM

/// Decides whether an instance's slot takes provisioning options. Nothing
/// calls it yet: once wb-vmd-install adds provisioning options to a start,
/// wb-vmd runs it before it sets them on the VM (#629).
public enum ProvisioningCheck {
    /// Accepts provisioning options in `slot`, or throws
    /// `vmd-provisioning-refused`. Both decisions are logged with the slot,
    /// and the refusal with its rule.
    public static func check(slot: VMSlot, log: EventLog) throws(VMRefusal) {
        guard slot.acceptsProvisioning else {
            log.refusal(
                "provisioning-refused", rule: VMRule.provisioningRefused.rawValue,
                ["slot": .string(slot.rawValue)])
            throw VMRefusal(
                .provisioningRefused, .permissionDenied,
                "the \(slot.rawValue) slot does not accept provisioning options")
        }
        log.info("provisioning-accepted", ["slot": .string(slot.rawValue)])
    }
}
