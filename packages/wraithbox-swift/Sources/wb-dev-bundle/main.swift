// wb-dev-bundle: dev-only test tooling, never in the app bundle
// (S10-tech-stack, "Test tooling"). It makes, clones, provisions, stops and
// removes test VM bundles in the spike cache, for the `mise run vm:*`
// tasks (docs/agents/host-work.md, "VM bundles"). It is promoted from the
// X18-vsock-handoff host tool, and signed ad hoc with
// spikes/vm.entitlements by `mise run vm:build`.
//
// The Virtualization framework calls back on the main queue, so the
// command runs on the main actor with the main run loop running, as in
// the X18 tool.
import Foundation

let arguments = Array(CommandLine.arguments.dropFirst())
Task { @MainActor in
    let log = Log.standardError
    do throws(Refusal) {
        let cache = try SpikeCache.forCurrentUser()
        exit(await Command.run(arguments, cache: cache, log: log))
    } catch {
        log.refused(error)
        exit(1)
    }
}
RunLoop.main.run()
