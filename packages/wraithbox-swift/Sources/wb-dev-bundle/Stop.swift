// `stop` (I350), and how a running `install` or `provision` hears it.
import Foundation

/// How long `stop` waits for the VM's process to let go of the bundle.
/// The process asks the guest to shut down, and stops the VM itself after
/// `StopRequest.guestGrace`.
let stopWait: Duration = .seconds(90)

/// Asks the process that runs the bundle's VM to stop it, with SIGTERM,
/// and waits until the process lets go of the bundle's lock. Returns the
/// exit status: 0 when nothing runs or the VM stopped, 1 when the process
/// still holds the bundle after `wait`.
///
/// The lock file's descriptor is close-on-exec and the tool starts no
/// child process, so the process that holds the lock is the one whose ID
/// the lock file holds.
@MainActor
func stopBundle(
    _ bundle: CachePath, log: Log, wait: Duration = stopWait,
    signal: (pid_t) -> Int32 = { kill($0, SIGTERM) }
) async -> Int32 {
    guard let pid = BundleLock.holder(bundle: bundle.path) else {
        log.step("\(bundle.path): no VM runs from it")
        return 0
    }
    guard pid > 1, pid != getpid() else {
        log.step("\(bundle.path): the lock file names process \(pid), which isn't signaled")
        return 1
    }
    log.step("\(bundle.path): sending SIGTERM to process \(pid)")
    guard signal(pid) == 0 else {
        log.step("\(bundle.path): kill \(pid): \(errnoText())")
        return 1
    }
    let clock = ContinuousClock()
    let deadline = clock.now + wait
    while clock.now < deadline {
        if BundleLock.holder(bundle: bundle.path) == nil {
            log.step("\(bundle.path): stopped")
            return 0
        }
        try? await Task.sleep(for: .milliseconds(200))
    }
    log.step("\(bundle.path): process \(pid) still runs its VM after \(wait)")
    return 1
}

/// SIGTERM and SIGINT, as a flag that a running command polls.
@MainActor
final class StopRequest {
    /// How long a provisioning boot waits for the guest to shut down after
    /// a stop request, before it stops the VM.
    static let guestGrace: Duration = .seconds(30)

    private(set) var requested = false
    private var sources: [any DispatchSourceSignal] = []

    /// Starts listening. `onStop` runs on the main queue at the first
    /// signal.
    init(onStop: @escaping @MainActor () -> Void = {}) {
        for number in [SIGTERM, SIGINT] {
            Darwin.signal(number, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: number, queue: .main)
            source.setEventHandler { [weak self] in
                MainActor.assumeIsolated {
                    guard let self, !self.requested else {
                        return
                    }
                    self.requested = true
                    onStop()
                }
            }
            source.resume()
            sources.append(source)
        }
    }
}
