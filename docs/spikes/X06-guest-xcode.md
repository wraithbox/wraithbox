# X06 - Guest users and Xcode

**Purpose:** Whether Xcode builds, tests and simulators run for a project user that `wb-guestd` starts from root without a GUI login, and which Seatbelt exceptions they need.

Brief: B20-guest-xcode

## For review

- **Decides:** the answer to X06-guest-xcode (I20), which says what
  FR06-native-tools can offer with full Xcode, and which work needs a
  GUI login session (S06-04-vms-and-placement).
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
  3. How a project that uses simulators is profiled (B20-guest-xcode,
     decision 2, I196). As specified now, no Layer 2 profile runs
     simulators, so a confined session can't use them. Recommended: option 2A, a simulator profile for
     projects that declare simulators, as described in "The draft
     profile and its exceptions" below. The alternative is to keep
     looking for a narrower profile.
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
   session helper's POSIX session doesn't hold them. In the g3 run, a
   booted simulator brought the project user to 308 processes. In the
   final run, killing every process of the user in a loop found 218,
   then 11, then none. None came back in 5 seconds, and
   `launchctl bootout user/<uid>` then returned 0 with no process of
   the user left. The script's next step printed the user's domain with
   `launchctl print`. Two later checks found one process of the user,
   `distnoted agent`, started by launchd 11 s before the second check,
   with no session running (`results/final-setup.jsonl`, `killuser`,
   `mem-after-kill`, `ps-after-kill`). What started it, the `print` or
   something else, wasn't recorded. Whether a booted-out domain stays
   down while the user is locked is not checked yet (I196).

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
(`guest/xcode.sb`). S13-guest-confinement's build profile takes the
build rows. The rest belong to option 2A below.

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
the log showed no Seatbelt denial. The runs' outputs and exit codes
are in `results/g3-bisect-tasks.jsonl`, but the profile of each run
wasn't kept, so the sets below come from the spike's notes and are not
recorded.
The one lead is `xcodebuild`'s own message "attempt to post
distributed notification 'IDETestProgressNotification' thwarted by
sandboxing", which it logs for any sandboxed process (I196). The
narrower sets tried all hung: `file*` alone, `file*` with
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

**Option 2A, a simulator profile** (B20-guest-xcode, decision 2).
Only a project that declares simulators would get it. That
declaration is a host-side project setting that `wb` sets, never read
from `.wraithbox/`, even with `config_trust` (SEC09-host-policy). On
top of the build profile of S13-guest-confinement, it would add:

- reads of the `cryptexd` runtime mounts
  (`/private/var/run/com.apple.security.cryptexd/mnt`) and the
  metadata of `/Volumes`;
- lookups of CoreSimulator's services,
  `com.apple.CoreSimulator.simdiskimaged`,
  `com.apple.PowerManagement.control`, distributed notifications and
  CoreDevice;
- the `IOSurfaceRootUserClient` and `AppleParavirtDeviceUserClient`
  user clients;
- `process-info-pidinfo` and the `procargs` sysctls, with the base's
  deny left out;
- p2's allow of nearly every operation class, for `xcodebuild test`.

Unfiltered `pidinfo` shows the session every process in the VM with
its executable path. That the guest kernel still hides the arguments
and environment of other users' processes from `procargs` is
unverified: the spike didn't test it. Under this profile only the
`AF_VSOCK` deny is left, and it wasn't checked again under it, so the
generator's tests would run the X27-vsock-confinement check on every
profile they generate.

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

- **S13-guest-confinement**, "Layer 2": the Xcode build profile, which
  runs no simulators, and in "Known gaps" the simulator processes
  outside every profile and agents launchd starts again. B20-guest-xcode
  decision 2 would add a simulator profile. "Layer 1": where
  `wb-guestd` kills every process of a project user, the kill loop and
  a `bootout` of the user's launchd domain repeat until none is left,
  for at most 10 rounds and 30 seconds. Both bounds are chosen, not
  measured: the spike's loop needed two rounds. Past them `wb-guestd`
  logs the failure, leaves the user locked and reports the project as
  not cleaned up (changed in this pull request).
- **The build profile** in S13-guest-confinement wasn't run as
  written: the spike's p1 also had the simulator rules. The
  generator's tests run every build task under it (I196).
- **S07-egress-gateway**, "Leftover processes": the same `bootout`
  (changed in this pull request).
- **S06-vm-lifecycle**, "VMs": every `xcodebuild test` on macOS and
  launching a macOS app need a GUI login session, which the work VM
  doesn't have (changed in this pull request). Where that work goes is
  open decision 1. "Images" is unchanged until I194 decides how Xcode
  and the runtimes get in.
- **S11-verification-and-spikes**, "Spikes": lists X06-guest-xcode
  (changed in this pull request).
- **FR06-native-tools** stays as it is. Full Xcode works for builds
  and SwiftPM under the Layer 2 profiles as specified. iOS simulators
  and their tests work only with decision 2's option 2A, outside those
  profiles.

## Spike code

Branch `spike/x06-guest-xcode`, at
[25dacb5](https://github.com/wraithbox/wraithbox/tree/25dacb5f65185a54bcbb1c973399ad8841d83061/spikes/x06-guest-xcode):
the Swift `wb-vmd` stand-in, the guest daemon with `runas`, the task
scripts, the generated test projects, the profiles and their
exceptions, and the raw results in `results/`. The spike's bundle `g1`
and its Xcode disk are spike artifacts, and no product image may
descend from them.

## Follow-up (I624): the host export and the Xcode disk

### For review

- **Decides:** nothing new. It measures the steps S06-14-xcode-image
  specifies, on the host and in a guest, and corrects the spec where a
  measurement contradicts it. After the attended steps, the license
  check drops the Xcode version, and the Xcode code requirement gets a
  third branch.
- **You are approving:** the corrections to S06-14-xcode-image listed
  in "What it means for the specs" below. The largest: an exported
  runtime is a directory with a signed disk image in it, and the
  runtime rules now check the directory and what it holds.
- **Controls touched:** none weakened. SEC12-least-privilege: the
  shim's profile needs more than `wb-diskfmt`'s. S06-15-xcode-disk
  names the additions and the one Mach service, `com.apple.trustd.agent`,
  and "The profile" below lists the measured rules.
  SEC13-bounded-resources: the Xcode disk is sized from file sizes, and
  the build VM needs room for every runtime. SEC15-no-guest-disk-mount:
  the shim mounts the blank volume twice, in `/Volumes` and on its own
  directory, and both mounts fall under the existing exception. The runtime's disk image has an
  Apple signature, which the shim now checks with `identifier image and
  anchor apple`, and a changed byte fails it. The Xcode requirement's
  new third branch uses `anchor apple`, which only Apple's own signing
  certificates meet, not `anchor apple generic`, which every Developer
  ID meets.
- **Assumed:** nothing beyond the measurements. The maintainer ran the
  attended steps on 2026-11-10, and their results settle what this
  section first left open: the license check compares the license ID
  only, and the code requirement has a third branch for the
  developer-site install.
- **Asked of the maintainer:** whether other local accounts on the Mac
  count as an adversary. T00-index trusts the host user's account, and
  S06-15-xcode-disk is written for that account only. If other
  accounts count, the shim's volume, while it is mounted, needs a
  closer look.

### Question

From I624, for S06-14-xcode-image as written:

1. What does `xcodebuild -downloadPlatform iOS -exportPath` write, and
   in what form?
2. Do the code requirements hold on the host's Xcode and the exported
   runtime, and does a changed byte fail them?
3. Where is the license record, and what does it hold?
4. Do the guest steps work with the network off and the Xcode volume
   read-only?
5. Does the build of the Xcode disk work under a profile like
   `wb-diskfmt`'s, and how long does each step take?

### Answer

1. **A directory per runtime.** The export wrote two directories, one
   per runtime, named `<platform>_<version>_<build>.exportedBundle`:
   `iphonesimulator_27.2_24B8117.exportedBundle` and
   `iphonesimulator_27.2_24B91016s.exportedBundle` (the second is the
   iPhone19-4 variant). Each is 8.3 GB. In each, `Restore/` holds one
   disk image, `BuildManifest.plist` and `Restore.plist`, and
   `Restore/Firmware/` holds the image's `.cryptex_info`, `.root_hash`
   and `.trustcache` files. `ExportedMetadata.plist` is at the top. The
   whole download and export took 542 s. The same run also installed
   both runtimes on the host: `xcrun simctl runtime list` there shows
   `iOS 27.2 (24B8117)` and `iOS 27.2 (24B91016s)`, both Ready, two
   disk images of 15.5 GB in total (attended step 2).
2. **Yes, with a third branch.** On the App Store Xcode,
   S06-14-xcode-image's first code requirement holds, and plain `anchor
   apple` exits 3. Xcode 27.2 from the developer site's `.xip` is signed
   differently: its leaf is Apple's Software Signing certificate, under
   Apple Code Signing Certification Authority and Apple Root CA, with
   team `59GAB85EFG`. Its designated requirement is `identifier
   "com.apple.dt.Xcode" and anchor apple`. The first and second
   branches exit 3 on it, and that requirement as a third branch exits
   0. The requirement with all three branches holds on both installs.
   `pkgutil --check-signature` calls the `.xip` "signed Apple Software",
   under Software Update, Apple Software Update Certification Authority
   and Apple Root CA. The runtime's disk image is signed with identifier
   `image` by Apple's macOS Software Signing certificate, and
   `identifier image and anchor apple` holds. One byte changed in a
   framework of a copy of the App Store Xcode fails the check with
   "invalid signature (code or signature have been modified)", and so
   does one byte changed in a copy of the disk image. `codesign` doesn't
   find a signature on the other files of an export.
3. **`/Library/Preferences/com.apple.dt.Xcode.plist`**, owned by
   root, mode 0644. It holds the accepted license's ID per type
   (`IDELastGMLicenseAgreedTo = EA2002`) and the Xcode version that
   accepted it (`27.0`, while the host runs 27.2). The app names its
   license in `Contents/Resources/LicenseInfo.plist`: `licenseID`
   `EA2002`, `licenseType` `GM`. Xcode 27.2 counts the record from
   27.0 as accepted: `xcodebuild -license check` exits 0 on the host
   (attended step 1).
4. **Yes, with another import command.** With every interface down
   and the volume mounted read-only, the copy, the signature check,
   `xcodebuild`, the license, the first launch and the runtime import
   all worked, and an iOS simulator booted on the runtime in 33 s.
   `xcodebuild -importPlatform` failed, both on the exported directory
   and on its disk image. It copied its input into root's temporary
   directory first. It then refused with "Cannot copy the image because
   the disk is almost full" at 22 GiB free, and left that copy behind.
   `xcrun simctl runtime add` on the disk image imported it in 46 s.
5. **Yes, with more rules.** A throwaway shim, confined by
   `selfsandbox` with `wb-diskfmt`'s profile plus the rules below, read
   the license record, checked Xcode and the runtime, made a 24 GiB
   image, formatted it, mounted it on a directory in the bundle, copied
   Xcode without `_MASReceipt` and one runtime into it, checked the
   copy and ejected it. A read outside its parameters was refused.

### Measurements

Host: Apple M2, 16 GB, macOS 27.2 (26B123), Xcode 27.2 (27B52) from
the App Store, in `/Applications`, 4.0 GB on disk and 10 GB of file
sizes (APFS compression). Guest: an APFS clone of the dev base bundle,
macOS 27.2 (26B123), 4 vCPU, 4 GiB, a 64 GiB system disk, and the
shim's `xcode.img` as its second disk. The guest had a NAT network for
SSH, and the script turned every interface but `lo0` down before each
step below. `curl` then failed with "no route".

On the host, unconfined:

| Command | Result | Time |
|---|---|---|
| `codesign --verify --deep --strict -R=<S06-14 requirement> Xcode.app` | exit 0 | 14.5 s |
| the same with `-R='anchor apple'` | exit 3, "code failed to satisfy specified code requirement(s)" | 15.2 s |
| the same on a `ditto` copy without `_MASReceipt` and extended attributes | exit 0 | 15.0 s |
| the same on a copy with one byte of `IDEFoundation` changed | exit 1, "invalid signature (code or signature have been modified)" | |
| the three-branch requirement on the App Store Xcode | exit 0 | 37.3 s |
| `codesign --verify --strict -R='identifier image and anchor apple'` on the runtime disk image | exit 0, `spctl`: accepted, source Apple System | 22 to 24 s |
| the same on a copy with one byte changed | exit 1, "invalid signature" | 22.6 s |

On Xcode 27.2 (27B52) expanded from the developer site's `.xip`, on the
host, unconfined:

| Requirement | Result | Time |
|---|---|---|
| first branch, App Store leaf | exit 3 | 15.2 s |
| second branch, Developer ID with Apple's team | exit 3 | 13.8 s |
| third branch, `identifier "com.apple.dt.Xcode" and anchor apple` | exit 0 | 15.9 s |
| all three branches | exit 0 | 20.4 s |
| `anchor apple generic and identifier "com.apple.dt.Xcode"`, never accepted, for reference | exit 0 | 67.7 s |

The attended steps on the host, run by the maintainer:

| Command | Result |
|---|---|
| `xcodebuild -license check`, record at `27.0`, Xcode 27.2 (27B52) | exit 0 |
| `xcrun simctl runtime list` | both iOS 27.2 runtimes Ready, 2 disk images, 15.5 GB |
| `pkgutil --check-signature` on the `.xip` | "signed Apple Software" |
| `codesign -d -r-` on the expanded app | leaf Software Signing, team `59GAB85EFG` |

The runtime list's output began with "Install Started" and "Install
Succeeded", and with a line from `CoreSimulator`: "Framework version
(1174.21) does not match existing job version (1174.9.2)". It is
recorded as seen. What caused it wasn't looked into.

The shim's steps, confined, over five runs:

| Step | Time |
|---|---|
| Deep check of the host's Xcode | 53 to 107 s |
| Check of one runtime disk image | 22 to 27 s |
| SHA-256 of one runtime disk image | 4.4 s |
| `diskutil image attach --noMount`, `eraseDisk`, `unmount`, `mount -mountPoint` | 0.25, 1.2, 0.3, 0.2 s |
| `ditto` of Xcode | 78 to 81 s |
| `ditto` of one runtime | 10 to 11 s |
| Deep check of the copy | 45 to 46 s |
| `unmount`, `eject` | 0.6, 0.2 s |

The copy on the volume took 10 GB, because `ditto` without
`--hfsCompression` writes files uncompressed. The 24 GiB image had
19.1 GB allocated after Xcode and one runtime. With
`--hfsCompression`, confined, `ditto` failed to write a file, still
exited 0, and the check of the copy failed with "code object is not
signed at all".

In the guest, as root:

| Step | Result | Time |
|---|---|---|
| `diskutil mount readOnly wb-xcode`, then a write | "Read-only file system" | |
| `ditto --norsrc --noextattr --noacl --noqtn` to `/Applications` | 167,224 entries owned by UID 99 | 88.9 s |
| `chown -R root:wheel`, `chmod -R go-w` | | 9.3 s, 4.2 s |
| Deep check of the copy with the requirement | exit 0 | 59 s |
| `spctl --assess` | accepted, source Mac App Store | |
| `xcodebuild -version` (first run) | Xcode 27.2, 27B52 | 43 s |
| `xcodebuild -license accept` | exit 0, writes the record with version `27.2` | 0.11 s |
| `xcodebuild -license check` | exit 0 | |
| `xcodebuild -runFirstLaunch` | "Install Succeeded" | 22.8 s |
| Check of the runtime disk image | exit 0 | 29.8 s |
| `xcodebuild -importPlatform` on the directory, 8.4 GiB free | "Cannot copy the image because the disk is almost full", exit 70 | 22 s |
| `ditto ... --hfsCompression` of Xcode, after removing the first copy | 4.0 GB, check of the copy exit 0 | 139.6 s |
| `xcodebuild -importPlatform` on the directory, 22 GiB free | the same message, a 7.7 GB copy left in root's temporary directory | 26 s |
| `xcrun simctl runtime add` on the disk image, 30 GiB free | `iOS 27.2 (24B8117) ... (Ready)`, 7.7 GB | 46 s |
| `xcrun simctl bootstatus -b` on an iPhone 18 Pro | Finished | 33 s |

`-runFirstLaunch` added three `com.apple.*` system jobs
(`CoreDevice.CoreDeviceDDIUpdaterService`,
`CoreSimulator.simdiskimaged`, `PerformanceTrace.PerformanceTraceService`)
and no file in `/Library/LaunchDaemons` or `/Library/LaunchAgents`.
Everything it wrote outside `/Users` is owned by root: the frameworks
in `/Library/Developer/PrivateFrameworks`, the developer disk images in
`/Library/Developer/DeveloperDiskImages`, and `CoreSimulator`'s
directories. No new file outside `/Users` was owned by a UID of 500
or more, so the sealing scan has nothing to refuse.

The import didn't add any file outside `/Users` owned by a UID of 500 or
more. Its disk image went to
`/Library/Developer/CoreSimulator/Cryptex/Images/Inbox`, owned by
root. The failed imports left a copy of their input in root's
temporary directory and one in that `Inbox`. A later `-importPlatform`
of the same input then failed with "an item with the same name already
exists".

### What was not measured

- **A Developer ID install.** The developer-site install is signed
  with Software Signing, so the second branch matched no install
  measured here.
- **A guest with no network device.** The guest had a NAT device with
  its interfaces turned down. `wb-dev-bundle` attaches a network to
  every VM it starts, and a start without one is a new option there.
- **The shim's file and `sysctl` rules are not minimal.** They are
  `wb-diskfmt`'s with every rule the runs needed added, and no rule was
  taken out to see whether it is needed. The Mach services were
  narrowed afterward ("The profile" below).
- **The launchd jobs' program paths.** The guest run listed the three
  new labels, not where their programs are.
- **An import without the runtime's `Firmware` files.**
- **Xcode's GUI**, and the iPhone19-4 runtime.

### The profile

The throwaway shim ran with `wb-diskfmt`'s profile plus these rules,
for I681 to start from:

- read `/Applications/Xcode.app`'s real path, `<data>/xcode-runtimes`
  and `/Library/Preferences/com.apple.dt.Xcode.plist`, and the metadata
  of `/Applications`;
- read, write, `file-mount` and `file-unmount` under `xcode-mnt` in
  the build bundle;
- `process-exec` of `/usr/bin/ditto` and `/usr/bin/codesign`;
- `mach-lookup` of `com.apple.trustd`, `com.apple.trustd.agent`,
  `com.apple.SecurityServer`, `com.apple.ocspd` and
  `com.apple.system.opendirectoryd.libinfo`;
- `sysctl-read` of `kern.bootargs`, `kern.osproductversion`,
  `kern.osvariant_status`, `kern.iossupportversion`,
  `hw.ephemeral_storage`, `security.mac.lockdown_mode_state` and
  `security.mac.amfi.qa_root_certs_allowed`;
- `system-info` `vfs.disk-space`;
- metadata of `/System/Cryptexes/OS`, `/System/Cryptexes/App`, `/etc`,
  `/var`, `/Users`, and each parent of the build bundle and of the runtimes folder, and read of
  `/usr/share/icu`.

The Mach services were then measured one by one with `sandbox-exec`,
with every file read and `sysctl-read` allowed, and `codesign` and
`ditto` run directly (I624's builder, and again in PR748's security
review):

| Profile's `mach-lookup` | Runtime disk image | Xcode, three branches |
|---|---|---|
| none | exit 1, `CSSMERR_TP_NOT_TRUSTED`, 0.1 s | exit 1, 1.0 s |
| `com.apple.trustd` only | exit 1, `CSSMERR_TP_NOT_TRUSTED` | |
| `com.apple.trustd.agent` only | exit 0, 26.1 s | exit 0, 16.7 s |
| both trust services | exit 0, 25.2 s | exit 0, 31.2 s |
| both, with a wrong requirement | | exit 3, 20.6 s |

`ditto --norsrc --noextattr --noacl --noqtn` of an exported runtime
didn't need a service, and the copy's disk image equals the source's. That
copy was on the same volume. So `com.apple.trustd.agent` is the one
service S06-15-xcode-disk allows. `com.apple.SecurityServer`, which
serves the keychain, `com.apple.ocspd` and the user database lookup
aren't needed for these checks.

The same security review measured what `codesign --verify --deep
--strict` with the requirement doesn't catch, on a `ditto` copy of an
App Store app signed like Xcode. A planted executable in
`Contents/_MASReceipt`, a new `_MASReceipt` in a nested `.appex` or
framework, a `.DS_Store` with arbitrary bytes in `Resources`, mode 6755
on the main executable, and mode 0777 on `Info.plist` and `Frameworks`
all passed with exit 0. A new file in `Resources` and a file in
`_CodeSignature` failed with exit 1. S06-15-xcode-disk removes
`_MASReceipt` and `.DS_Store` at any depth, and `install-xcode` refuses
them and clears setuid and setgid bits.

### Attended steps

`.scratch/attended/624-host.sh` on the branch's worktree, run by the
maintainer on 2026-11-10, each step writing a file that the builder
read. The `.xip` and the expanded app were deleted afterward:

```sh
{ xcodebuild -version; xcodebuild -license check; echo "license check rc=$?"; } 2>&1 | tee .scratch/m/attended-license-check.txt
xcrun simctl runtime list 2>&1 | tee .scratch/m/attended-host-runtimes.txt
pkgutil --check-signature .scratch/xip/Xcode_27.2.xip 2>&1 | tee .scratch/m/attended-xip-signature.txt
(cd .scratch/xip && xip --expand Xcode_27.2.xip)
codesign -d -r- -vvvv .scratch/xip/Xcode.app 2>&1 | grep -E 'Authority|designated|TeamIdentifier' | tee .scratch/m/attended-xip-codesign.txt
```

### What it means for the specs

S06-14-xcode-image, changed in this pull request:

- "Where the runtimes come from": the export is a directory per
  runtime. `xcode-runtime-name` checks the directory's name, the new
  `xcode-runtime-layout` checks that it holds only the files above,
  and the size cap counts the sum of the files' sizes. One export can
  write more than one runtime.
- "The checks on the host": the runtime's check is `codesign` with
  `identifier image and anchor apple` on its disk image. The code
  requirement for Xcode gets a third branch, `identifier
  "com.apple.dt.Xcode" and anchor apple`, for the developer-site
  install. The license record doesn't name a build, and Xcode accepts a
  record from an older version. The license check compares the license
  ID for the app's type only.
- "Where the runtimes come from": after the export, the host listed
  both runtimes as installed and `Ready`.
- "In the build VM": the guest checks each runtime's disk image before
  it imports it, refuses a receipt, a `.DS_Store` or a file not owned
  by root in the copy, and clears setuid and setgid bits. The sealing
  scan accepts the three launchd jobs of `-runFirstLaunch`, and only in
  an Xcode image.
- "In the build VM": the guest copies Xcode with `--hfsCompression`
  and sets the owner itself, imports each runtime with `xcrun simctl
  runtime add` on its disk image, and needs room for Xcode and every
  runtime on its system disk.

S06-15-xcode-disk, new in this pull request, takes "The Xcode disk"
and "What the build records" from S06-14-xcode-image, which had grown
past a page's length:

- the image is sized from the sum of `st_size`, not from blocks;
- `eraseDisk` mounts the new volume, and the shim then mounts it on a
  directory in the bundle. It finds the volume by device only, and
  requires an empty volume before the copy and only what it copied
  before the unmount;
- the copy drops `_MASReceipt` and `.DS_Store` at any depth;
- the copy is checked again, runtimes included, and the reported
  values come from the copy;
- the profile's additions are named, with `com.apple.trustd.agent` as
  the one Mach service, and the timings above set the deadlines;
- the build records the SHA-256 of each copied runtime disk image.

### Spike code

The throwaway shim (`main.go`, `profile.go`) and the guest scripts
stayed in the builder's worktree under `.scratch/`, and were not
committed.

**Status:** Answered 2026-10-08: yes, with conditions. Follow-up measured 2026-11-10 (I624): the export is a directory per runtime with a signed disk image, both code requirements hold and fail on a changed byte, the confined shim builds the Xcode disk, and the guest steps work offline with `simctl runtime add`. Attended steps 2026-11-10: Xcode accepts an older license record, and the check compares the license ID only. The developer-site install needs a third requirement branch, `identifier "com.apple.dt.Xcode" and anchor apple`
