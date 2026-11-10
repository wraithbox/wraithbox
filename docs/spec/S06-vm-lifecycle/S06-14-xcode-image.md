# S06-14 - The Xcode Image

**Purpose:** How an image gets full Xcode and the simulator runtimes
from the host, as a read-only disk the host builds, and who runs
Xcode's first launch.

**Requirements:** FR06-native-tools, SEC02-no-host-fs-share,
SEC04-no-guest-secrets, SEC05-default-deny, SEC09-host-policy, SEC12-least-privilege,
SEC13-bounded-resources, SEC15-no-guest-disk-mount

**Tests:** C09-guest-and-sessions, C11-host-daemons

Brief: B194-xcode-image

## Design

### Where Xcode comes from

Full Xcode isn't in the base image (S06-01-image-layers, "Layers"). A
named image whose recipe asks for Xcode (I150) gets it in an *Xcode
step*. The recipe holds only a flag that asks for Xcode, never a path.
Recipes come from user settings only, never from a repository's
`.wraithbox/` (I150, SEC09-host-policy).

The Xcode step clones the sealed base image, installs Xcode and the
runtimes, and is sealed. After `wb-vmd-layer` exits, `wb-hostd` takes
that sealed result into a new, empty build bundle as in
S06-02-image-build, "Taking the sealed image". It is never listed as an
image. The recipe's own steps then run on it, in a VM started without
the Xcode flag, so they can use Xcode. The image they seal is the
parent that the Claude Code layer clones (S06-01-image-layers, "The
Claude Code layer"). When the recipe has no other step, the Xcode
step's sealed result is that parent.

The image build copies the Xcode installed on the host. It never
downloads Xcode, because Apple serves Xcode only after an Apple ID
sign-in, and the build has none. It never takes Xcode from the network
in the guest either.

The source is `/Applications/Xcode.app`. Its real path has to be a
directory directly in `/Applications` whose name ends in `.app`, so a
link from `Xcode.app` to `Xcode-27.0.app` works. Any other source is
refused with `xcode-not-found`.

### Where the runtimes come from

The simulator runtimes come from the host too. The guest never
downloads one:

- The build VM of the Xcode step has no network device
  (S04-04-vm-processes, "`wb-vmd-layer`").
- The runtime download reaches Apple hosts that are expected to refuse
  an inspected leaf (S07-07-hosts-and-modes, "Known to break under
  inspection"), and Wraith Box never puts those in pass mode.

The user exports each runtime on the host, with no Apple ID
(X06-guest-xcode):

```sh
xcodebuild -downloadPlatform iOS -exportPath "<data>/xcode-runtimes"
```

The command downloads every runtime Apple lists for the platform, and
writes each as an *exported runtime*, which is a directory
(X06-guest-xcode, "Follow-up (I624)"). For iOS 27.2 it wrote two
of about 8.3 GB each, one general runtime and one for a newer device.
After the export, the host listed both as installed and `Ready`, 15.5
GB in all. Wraith Box neither uses nor removes them. An exported
runtime looks like this:

```text
iphonesimulator_27.2_24B8117.exportedBundle/
  ExportedMetadata.plist
  Restore/141-80522-035.dmg
  Restore/BuildManifest.plist
  Restore/Restore.plist
  Restore/Firmware/141-80522-035.dmg.cryptex_info
  Restore/Firmware/141-80522-035.dmg.root_hash
  Restore/Firmware/141-80522-035.dmg.trustcache
```

The build takes the exported runtimes in `<data>/xcode-runtimes`. With
none there, the image has Xcode and no simulator runtime, and `wb image
build` says so. `wb-hostd` never runs `xcodebuild` on the host.

Each entry in the folder, and each entry under it, is opened with
`O_NOFOLLOW`, and the build refuses:

- an entry that isn't a directory named
  `<platform>_<version>_<build>.exportedBundle`, with a lowercase
  platform such as `iphonesimulator` (`xcode-runtime-name`);
- an exported runtime with anything but the layout above: the named
  regular files and directories, and exactly one disk image in
  `Restore`, whose name the three `Firmware` files repeat
  (`xcode-runtime-layout`);
- more exported runtimes than a count cap (`xcode-runtime-too-many`).
  One export can write more than one;
- exported runtimes whose files' sizes (`st_size`) add up to more than
  a size cap (`xcode-runtime-too-large`).

The caps are in code.

### The checks on the host

Before it copies anything, the shim of S06-15-xcode-disk checks,
and refuses with the rule:

- that Apple signed the app and that its sealed parts are unchanged
  (`xcode-signature-invalid`), with `codesign --verify --deep --strict`
  against this code requirement:

  ```text
  (anchor apple generic and identifier "com.apple.dt.Xcode" and
   (certificate leaf[field.1.2.840.113635.100.6.1.9] or
    (certificate 1[field.1.2.840.113635.100.6.2.6] and
     certificate leaf[field.1.2.840.113635.100.6.1.13] and
     certificate leaf[subject.OU] = "59GAB85EFG")))
  or (identifier "com.apple.dt.Xcode" and anchor apple)
  ```

  The first branch is Apple's App Store signature, and the second its
  Developer ID with Apple's own team. The third is the `.xip` from
  Apple's developer site, signed with Apple's own Software Signing
  certificate. `anchor apple generic` without the leaf or team clause is
  never accepted, because every certificate Apple issues, any
  developer's Developer ID included, meets it;
- that the host's user accepted the license this Xcode names, from the
  host's own record of it (`xcode-license-not-accepted`);
- that Xcode's minimum macOS version is at most the parent image's
  macOS version (`xcode-too-new-for-guest`);
- that Apple signed each exported runtime's disk image and that it is
  unchanged (`xcode-runtime-signature-invalid`), with
  `codesign --verify --strict` against this code requirement:

  ```text
  identifier image and anchor apple
  ```

  The disk image has its own code signature, made with Apple's "macOS
  Software Signing" certificate, with one hash over the whole image. The
  other files of an exported runtime have no signature the host can
  check. The guest's import reads only the disk image ("In the build
  VM");
- that `<data>` has room for the disk and the image
  (SEC13-bounded-resources, `xcode-disk-no-space`).

On the host's App Store Xcode and on Xcode 27.2 expanded from the
developer site's `.xip`, the code requirement above holds. The `.xip`
install's designated requirement is `identifier "com.apple.dt.Xcode"
and anchor apple`, and the first and second branches alone fail on it
(`codesign` exits 3). Plain `anchor apple` fails on the App Store
Xcode. A copy with one byte changed in a framework fails, as does a
runtime disk image with one byte changed (X06-guest-xcode, "Follow-up
(I624)").

The license record is `/Library/Preferences/com.apple.dt.Xcode.plist`,
which `xcodebuild -license accept` writes as root, and which is the
Xcode entry's read rule for it. It holds the accepted license's ID per
license type, `IDELastGMLicenseAgreedTo` for a release, and the Xcode
version that accepted it, `IDEXcodeVersionForAgreedToGMLicense`. It
doesn't name a build. The app names the license it needs in
`Contents/Resources/LicenseInfo.plist` (`licenseID`, `licenseType`).

The shim parses both plists itself. It refuses unless the ID recorded
for the app's license type equals the app's `licenseID`. A fixed list
in code maps the type to the record's key, and holds only the measured
one: `GM` to `IDELastGMLicenseAgreedTo`. Any other type, a beta's
included, refuses with `xcode-license-not-accepted`. It doesn't
compare the recorded version, because Xcode doesn't either: on the
reference machine the record says `27.0`, Xcode 27.2 is installed,
and `xcodebuild -license check` exits 0 (X06-guest-xcode, "Follow-up
(I624)"). A new license ID still needs a new acceptance, which the
refusal names: `sudo xcodebuild -license accept`.

### In the build VM

The Xcode step boots its clone in `wb-vmd-layer`, with no network
device, cloned as for the Claude Code layer (S06-01-image-layers). Its
device set adds the Xcode disk, read-only, and only for this step
(S06-06-devices-and-warm-start, "Devices"). `wb-vmd-layer` attaches
`xcode.img` from the build bundle only when the start call sets its
Xcode flag. The call never names a path. A start call with the Xcode
flag in any other slot is refused before any device is created, and
logs `vmd-xcode-disk-refused`. The flag is never dropped to let the
start go on (S04-10-vmd-install, "The build bundle", and
S06-06-devices-and-warm-start, "The Xcode disk").

A named `wb-guestd` operation, `install-xcode`, then runs as root
(S06-02-image-build, "No guest disk mounts"):

1. It checks the volume at `/Volumes/wb-xcode` as it checks the guest
   tools disk: APFS, read-only, and mounted by root
   (S06-12-guest-agent, "Update at start").
2. It copies `Xcode.app` to `/Applications/Xcode.app` with
   `ditto --norsrc --noextattr --noacl --noqtn --hfsCompression`, then
   sets owner `root:wheel` with `chown -R` and takes write permission
   from group and others with `chmod -R go-w`. The files on the volume
   belong to user 99, and the sealing scan refuses files outside
   `/Users` that a non-system user owns (S06-02-image-build,
   "Sealing"). `--hfsCompression` keeps the copy at the 4.0 GB it
   takes on the host instead of 10 GB;
3. It checks the copy's signature with the same requirement. That
   check accepts a planted `_MASReceipt` or `.DS_Store` and changed
   mode bits (X06-guest-xcode, "Follow-up (I624)"). So the step also
   refuses a copy that holds either name at any depth, or a file not
   owned by `root` (`xcode-copy-invalid`), and clears every setuid and
   setgid bit.
4. It runs `xcode-select -s`, `xcodebuild -license accept` and
   `xcodebuild -runFirstLaunch`.
5. It checks each runtime's disk image, the one `.dmg` in its
   `Restore/`, with `identifier image and anchor apple`
   (`xcode-runtime-signature-invalid`, 29.8 s measured). It imports it
   with `xcrun simctl runtime add`, and checks
   that `xcrun simctl runtime list` then shows it as `Ready`.
6. It unmounts the volume.

These steps worked, and an iOS simulator then booted on the imported
runtime (X06-guest-xcode, "Follow-up (I624)"). They were measured in a
guest with a network device whose interfaces were all down. I684 runs
them without a network device. A failure there fails the build, and
the build doesn't add a device to get past it. `xcodebuild -importPlatform` didn't work there.
It first copied its input into root's temporary directory, then
refused for lack of room with 22 GiB free, and left the copy behind.
`simctl runtime add` copies the disk image once. The clone of the
Xcode step needs room on its system disk for Xcode and every runtime.

A failure fails the build. The host bounds and escapes the failure
text as for terminfo (S06-02-image-build, "Terminfo"). Then the image
is sealed as any other. `-runFirstLaunch` adds three root launchd
jobs with no file in `/Library/LaunchDaemons`:
`com.apple.CoreDevice.CoreDeviceDDIUpdaterService.job`,
`com.apple.CoreSimulator.simdiskimaged` and
`com.apple.PerformanceTrace.PerformanceTraceService`. The sealing scan
accepts these labels only in an image with the Xcode step, and only
for a program under `/Library/Developer/PrivateFrameworks` that root
owns (S06-02-image-build, "Sealing"). X06-guest-xcode didn't record
the program paths. I683's run records them, and they are in code.

### Who runs the first launch

Root runs `-runFirstLaunch`, once, in the image build, before the
image is sealed. It installs the CoreSimulator and CoreDevice
frameworks into `/Library/Developer`, and only root can write there.
The Layer 2 build profile reads them (S13-02-filesystem-profile,
"Xcode build profile").

A project user, the agent included, isn't an admin
(S06-08-projects-and-sessions), so a session can't run it. A session
never needs to: the image has the frameworks and the license record
before any project code runs.

### Kept on this Mac

Xcode is Apple's software under Apple's license, which the user
accepted on the host. An image with Xcode, and every image built on
it, is never shared with another machine. Sharing images between
machines is a later addition (S06-02-image-build, "Storage and
distribution"), and it leaves these images out. `wb image build` says
once, before it copies, that it copies the host's Xcode into the
image.

## Decisions

### Xcode from a read-only disk the host builds

- **Why:** downloading Xcode needs an Apple ID sign-in, which the
  build can't do. A disk image keeps SEC02-no-host-fs-share, which a
  shared directory would break.
- **Rejected:** downloading Xcode into each image.
- **Decided:** I194, 2026-10-16.

### Runtimes exported on the host

- **Why:** the build VM of the Xcode step has no network, and the
  runtime hosts are expected to refuse inspection. Pass mode for them
  would make each one a T01-allowed-channels channel.
- **Rejected:** `xcodebuild -downloadPlatform iOS` in the guest, as
  X06-guest-xcode did over a NAT network.
- **Rejected:** a `wb-download` entry for Apple's runtime catalog. No
  spike has looked at that catalog or how its files are signed.
- **Decided:** I194 (B194-xcode-image).

### Root runs the first launch in the build

- **Why:** it writes `/Library/Developer`, and no project user may.
- **Rejected:** a first launch at session start, which would need an
  admin user in the session.
- **Decided:** I194 (B194-xcode-image).

**Status:** Draft
