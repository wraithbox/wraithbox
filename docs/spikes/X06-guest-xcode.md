# X06 - Guest users and Xcode

**Purpose:** Whether Xcode builds, tests and simulators run for a project user that `wb-guestd` starts from root without a GUI login, and which Seatbelt exceptions they need.

Brief: B20-guest-xcode

## For review

- **Decides:** the answer to X06-guest-xcode (I20), which says what
  FR06-native-tools can offer with full Xcode, and which work needs a
  GUI login session (S06-vm-lifecycle, "VMs").
- **You are approving:** the answer "yes, with conditions". Without a
  GUI login, a project user can build macOS and iOS apps, build and
  test SwiftPM packages, sign to run locally, boot simulators, and run
  iOS unit and UI tests on them. Every `xcodebuild test` on macOS, a
  SwiftPM package's included, and launching a macOS app need a GUI
  session. Only `swift test` runs macOS tests without one. Whether the
  isolated VM's console user takes that work is open decision 1, and
  that fallback is not measured (I195).
- **Controls touched:** none of the `SEC*` controls changes. Layer 2
  of S13-guest-confinement is weaker than its text implied: a simulator
  runs outside the session's profile, and `xcodebuild test` on a
  simulator only finished under a profile that allows nearly every
  operation. Layer 2 adds to the host floor and never replaces it, so
  SEC08-proj-isolation still rests on the project user, which the
  spike kept. S13-guest-confinement now says so (changed in this pull
  request).
- **Assumed:** that the product image can get Xcode and the simulator
  runtimes the way the spike did, by copying the host's Xcode from a
  read-only disk and downloading the runtime with no Apple ID (I194).
  That a console session for macOS app tests can be had without a
  stored password in the image (I195).
- **Open decisions:**
  1. Whether v1 offers macOS tests through `xcodebuild` and macOS app
     launches at all, and if so how the isolated VM gets a console
     session (I195). Recommended: not in v1. Nothing tells `wb-hostd`
     before a session that it will run such a test, so there is nothing
     to refuse it on. Instead the limit is documented, and `wb` turns
     the `testmanagerd` error ("com.apple.testmanagerd.control was
     invalidated") into an explained message that names it
     (NFR06-explained-refusals).
  2. How the Xcode image variant gets Xcode and the runtimes (I194).
  3. The simulator profile: S13-guest-confinement now gives it only to
     projects that declare simulators, and says it doesn't confine the
     file system. The alternative is to keep looking for a narrower
     profile (I196). Recommended: accept it as specified.
- **Brief:** B20-guest-xcode

## Question

From X00-index: can builds and simulators run for a guest user without
a GUI login? If not, Xcode sessions go to the isolated VM's console
user. I20 asks for `xcodebuild build` and `test` of a macOS app and an
iOS app, a simulator booted with `simctl` with tests run on it, SwiftPM
with a resource bundle, and code signing to run locally, each as a
non-admin user started from a root daemon. Then the same under a draft
Seatbelt profile, noting the exceptions it needs.

## Answer

**Yes, with conditions.** For a project user with no GUI login,
everything runs except every `xcodebuild test` on macOS (a SwiftPM
package's included, where only `swift test` works) and launching a
macOS app. Each task, from the final run on a
fresh boot (`results/final-matrix.txt`):

| Task | `setuid` | `asuser` | Under the draft profile |
|---|---|---|---|
| SwiftPM build and test, resource bundle | yes, 48 s | yes, 280 s | yes, p1 |
| `xcodebuild build`, macOS app (App Sandbox) | yes, 14 s | yes, 13 s | yes, p1 |
| `xcodebuild build`, iOS app for the simulator | yes, 12 s | yes, 17 s | yes, p1 |
| `xcodebuild build`, iOS app for a device, unsigned | yes, 9 s | yes, 9 s | not run |
| Sign to run locally (ad hoc), and with a self-signed identity in a keychain the user creates | yes | yes | yes, p1 |
| `xcodebuild test`, macOS app, hosted unit tests | **no** | **no** | not run |
| `xcodebuild test`, macOS UI tests | **no** | **no** | not run |
| `xcodebuild test`, SwiftPM package on macOS | **no** | **no** | not run |
| `simctl` create, boot, install, launch, screenshot | yes, 143 s | yes, 166 s | yes, p1 |
| `xcodebuild test`, iOS hosted unit tests, simulator | yes, 35 s | yes, 31 s | p1: **hangs**. p2: yes |
| `xcodebuild test`, iOS UI tests, simulator | yes, 46 s | yes, 20 s | p2: yes |

`setuid` is how S06-vm-lifecycle's session helper starts a session: the
child's user, group and groups set directly, a new POSIX session, and
a PTY as its controlling terminal. `asuser` goes through
`launchctl asuser <uid>` first, which puts the child in launchd's
per-user domain (`user/<uid>`, session type Background). A
`setuid` child stays in the system domain. The two answered the same
for every task. Without a PTY, the iOS tests passed and the macOS tests
failed the same way.

1. **Builds, SwiftPM and signing: yes.** No step needed a login, a
   keychain unlock prompt, or an Apple ID. The macOS app is signed ad
   hoc with its App Sandbox entitlements and verifies with
   `codesign --verify --strict`. A user can create a keychain in its
   home, import a self-signed code signing identity and sign with it,
   all from the command line.
2. **Running a macOS app: no.** `open` fails with "Domain does not
   support specified action", because there's no `gui/<uid>` domain.
   Started directly, the App Sandbox app crashed in the system domain
   and kept running without a window in the Background domain.
3. **macOS tests through `xcodebuild`: no.** `testmanagerd`'s
   LaunchAgent is limited to the `LoginWindow` and `Aqua` session types,
   so `xcodebuild test` fails with "The connection to service named
   com.apple.testmanagerd.control was invalidated". Loading a copy of
   that agent into the user's Background domain made the SwiftPM
   package's tests pass through `xcodebuild`, but a hosted test still
   failed: RunningBoard launches the test host app into the missing
   `gui/<uid>` domain. `swift test` doesn't use `testmanagerd` and
   works. The fallback the question names, the isolated VM's console
   user, is not measured (I195).
4. **Simulators: yes.** CoreSimulator starts on demand for the
   project user. Its services run in launchd's `user/<uid>` domain
   whichever way the session started, and render on the VM's
   paravirtualized GPU. The app showed its window in a screenshot
   (`results/g3-sim-setuid.jpg`). Hosted iOS unit tests and an iOS UI
   test passed.
5. **The simulator's processes are outside the session.**
   `CoreSimulatorService`, `launchd_sim` and the simulated device's
   processes have launchd as their parent and POSIX session 0, so the
   session helper's POSIX session doesn't hold them. A booted simulator
   brought the project user to 308 processes. In the final run,
   killing every process of the user in a loop cleared 218 in two
   rounds, none came back in 5 seconds, and
   `launchctl bootout user/<uid>` then returned 0 with no process of
   the user left. The script's next step printed the user's domain with
   `launchctl print`. Two later checks found one process of the user,
   `distnoted agent`, started by launchd 11 s before the second check,
   with no session running (`results/final-setup.jsonl`, `killuser`,
   `mem-after-kill`, `ps-after-kill`). What started it, the `print` or
   something else, wasn't recorded (I196).

## The draft profile and its exceptions

The draft (p0) is the agent-safehouse v0.12.0 default selection as
X27-vsock-confinement rendered it, with S13-guest-confinement's grants
added: reads of `/Applications/Xcode.app`, reads and writes in the
project user's home and worktree, and the vsock rule last. It runs the
task under `sandbox-exec`. Each exception was added after reading the
sandbox denials of a failed run.

Under p0, `xcodebuild` refuses to start (exit 69), and `simctl` can't
load CoreSimulator. SwiftPM passed in the final run. In the first run
it failed the license check, which reads
`/Library/Preferences/com.apple.dt.Xcode.plist`.

**p1, what builds, SwiftPM, signing and `simctl` need**
(`guest/xcode.sb`):

| Exception | For |
|---|---|
| read `/Library/Preferences/com.apple.dt.Xcode.plist` | the license check of `xcodebuild` and `swift` |
| read `/Library/Developer` | the CoreSimulator and CoreDevice frameworks that `-runFirstLaunch` installs. `xcodebuild` doesn't start without them |
| `mach-lookup` `com.apple.SecurityServer`, `com.apple.security.syspolicy`, and read `/Library/Keychains` | a keychain in the user's home, and `codesign` with an identity in it |
| `mach-lookup` CoreSimulator's XPC services and `com.apple.CoreSimulator.simdiskimaged` | `simctl` and `xcodebuild` reaching the simulator service |
| read `/private/var/run/com.apple.security.cryptexd/mnt`, metadata of `/Volumes` | the mounted simulator runtime |
| `iokit-open-user-client` `IOSurfaceRootUserClient`, `AppleParavirtDeviceUserClient` | rendering |
| `mach-lookup` `com.apple.PowerManagement.control` | `xcodebuild test` refuses to start without a sleep assertion |
| `mach-lookup` `com.apple.distributed_notifications@1v3`, the CoreDevice XPC service | test progress notifications |
| `process-info-pidinfo` and the `procargs` sysctls, and the base's `(deny process-info-pidinfo)` left out | `xcodebuild` watching the test runner |

The safehouse base denies `process-info-pidinfo` with a rule that a
later `(allow process-info-pidinfo)` doesn't undo. Even `(allow
default)` after it left `xcodebuild test` hanging. With that one rule
removed and `(allow default)` added, the test passed, so the generator
has to leave the rule out rather than allow pidinfo after it.

**p2, what `xcodebuild test` on a simulator needs**
(`guest/xcode-simtest.sb`). With p1 plus `file-read*`,
`file-issue-extension`, any `mach-lookup` and outbound Unix sockets,
the tests ran
and passed, but `xcodebuild` didn't exit until the spike killed it, and
the log showed no Seatbelt denial (`results/g3-bisect-tasks.jsonl`).
The one lead is `xcodebuild`'s own message "attempt to post
distributed notification 'IDETestProgressNotification' thwarted by
sandboxing", which it logs for any sandboxed process (I196). The narrower sets tried all hung: `file*` alone, `file*` with
`mach*` and `network*`, `file*` with `mach*`, `ipc*` and `process*`,
and `file*` with `network*`, `signal`, `iokit*`, `sysctl*` and
`system*`. What finished was an allow of nearly every operation class:
`file*`, `mach*`, `ipc*`, `network*`, `signal`, `process*`, `iokit*`,
`sysctl*`, `system*`, `user-preference*` and a few more. With `file*`
allowed, the profile no longer confines the file system.

**The simulator leaves the profile.** Under p1, the session's own
write to `/Users/Shared` was refused, and the same write through
`xcrun simctl spawn <device> defaults write /Users/Shared/...`
succeeded, as the project user. The simulator's processes are started
by launchd, which S13-guest-confinement's "Known gaps" already names.
So any project that may use a simulator has no Layer 2 file
system confinement in practice. It keeps the project user's Unix
permissions, which is what SEC08-proj-isolation rests on.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0
(27A266a), no `sudo`. Guest: an APFS clone of X17-image-build's sealed
bundle, macOS 27.0.1, 4 vCPU, 8 GiB, one file-handle network attachment
with nothing behind it, plus a read-only raw disk with a copy of the
host's Xcode. The project user `x06p` was made with `sysadminctl
-addUser`, is in `staff` only (not `admin`), and never logged in.

| Step | Time |
|---|---|
| Copy Xcode (3.9 GB) from the read-only disk | 125 s |
| `xcodebuild -runFirstLaunch` as root | 62 s |
| `xcodebuild -downloadPlatform iOS` (arm64, 8.05 GB, NAT boot), no Apple ID | 1202 s |
| `simctl boot` to `bootstatus` finished | 44 to 70 s |
| Cold boot of the guest to the daemon answering | 9.7 to 11.8 s |

- SwiftPM under `asuser` took 280 s in the final run, against 48 s
  under `setuid`. The first run's `asuser` time wasn't recorded, and the
  final run had a booted simulator from the run before it. The spike
  didn't find out why.
- The hosts seen in the guest's log during the runtime download
  include `updates.cdn-apple.com`, `mesu.apple.com` and
  `gdmf.apple.com`, which S07-egress-gateway expects to refuse an
  inspected leaf. Which of them the download used was not measured.

## What was not measured

- **The console-user fallback** for macOS app tests. Automatic login
  for the project user writes `/etc/kcpassword`, which the sealing scan
  forbids, and the agent's permission policy refused turning it on in
  the spike clone (I195).
- **Signing with an Apple development certificate**, and building for
  a device with signing. Both need an Apple ID.
- **A narrower set for `xcodebuild test` on a simulator.** The spike
  stopped at p2 (I196).
- **watchOS, tvOS and visionOS simulators**, and Xcode's GUI.
- **The time a cold boot adds** before the first simulator boot, and
  the memory a booted simulator takes. The guest's free memory went
  from 88% to 82% of 8 GiB with one booted simulator.

## What it means for the specs

- **S13-guest-confinement**, "Layer 2": a build profile and a
  simulator profile with their exceptions, the safehouse rule the
  generator leaves out, the vsock check on every profile, and the
  simulator gap in "Known gaps". "Layer 1": `wb-guestd` boots out a
  project user's per-user launchd domain after its kill loop, and
  keeps it down while the user is locked (changed in this pull
  request). I196 measures whether it stays down.
- **S07-egress-gateway**, "Leftover processes": the same `bootout`
  (changed in this pull request).
- **S06-vm-lifecycle**, "VMs": every `xcodebuild test` on macOS and
  launching a macOS app need a GUI login session, which the work VM
  doesn't have (changed in this pull request). Where that work goes is
  open decision 1. "Images" is unchanged until I194 decides how Xcode
  and the runtimes get in.
- **S11-verification-and-spikes**, "Spikes": lists X06-guest-xcode
  (changed in this pull request).
- **FR06-native-tools** stays as it is. Full Xcode works for builds,
  SwiftPM and iOS simulators.

## Spike code

Branch `spike/x06-guest-xcode`, at
[25dacb5](https://github.com/wraithbox/wraithbox/tree/25dacb5f65185a54bcbb1c973399ad8841d83061/spikes/x06-guest-xcode):
the Swift `wb-vmd` stand-in, the guest daemon with `runas`, the task
scripts, the generated test projects, the profiles and their
exceptions, and the raw results in `results/`. The spike's bundle `g1`
and its Xcode disk are spike artifacts, and no product image may
descend from them.

**Status:** Answered 2026-10-08: yes, with conditions
