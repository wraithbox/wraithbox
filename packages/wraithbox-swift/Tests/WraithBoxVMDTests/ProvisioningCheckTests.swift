// C11-host-daemons, "Provisioning": every slot but `build` refuses
// provisioning options with vmd-provisioning-refused, and logs the rule.
// The check is called directly, so the layer slot's case doesn't depend
// on the start refusal of a slot whose entry isn't built yet.
import Testing
import WraithBoxVM

@testable import WraithBoxVMD

@Suite("Provisioning options")
struct ProvisioningCheckTests {
    @Test(
        "a slot other than build refuses provisioning options and logs vmd-provisioning-refused",
        arguments: [VMSlot.work, .isolated, .layer])
    func refused(slot: VMSlot) {
        let recorded = RecordedLog()
        let error = #expect(throws: VMRefusal.self) {
            try ProvisioningCheck.check(slot: slot, log: recorded.log)
        }
        #expect(error?.rule == VMRule.provisioningRefused.rawValue)
        #expect(error?.code == .permissionDenied)
        #expect(recorded.rules == ["vmd-provisioning-refused"])
        let records = recorded.events("provisioning-refused")
        #expect(records.count == 1)
        #expect(records.first?["slot"] as? String == slot.rawValue)
        #expect(recorded.events("provisioning-accepted").isEmpty)
    }

    @Test("the build slot accepts provisioning options and logs the decision")
    func accepted() throws {
        let recorded = RecordedLog()
        try ProvisioningCheck.check(slot: .build, log: recorded.log)
        #expect(recorded.rules.isEmpty)
        #expect(recorded.events("provisioning-accepted").first?["slot"] as? String == "build")
    }

    @Test("only the build slot accepts provisioning options", arguments: VMSlot.allCases)
    func onlyBuild(slot: VMSlot) {
        #expect(slot.acceptsProvisioning == (slot == .build))
    }
}
