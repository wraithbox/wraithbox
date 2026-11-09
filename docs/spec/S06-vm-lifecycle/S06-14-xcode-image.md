# S06-14 - The Xcode Image

**Purpose:** How an image gets full Xcode and the simulator runtimes
from the host, as a read-only disk the host builds, and who runs
Xcode's first launch.

**Requirements:** FR06-native-tools, SEC02-no-host-fs-share,
SEC04-no-guest-secrets, SEC09-host-policy, SEC12-least-privilege,
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
runtimes, and is sealed. The recipe's own steps then run on that sealed
result, so they can use Xcode. The image they seal is the parent that
the Claude Code layer clones (S06-01-image-layers, "The Claude Code
layer"). When the recipe has no other step, the Xcode step's sealed
result is that parent.

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

The build takes the runtime images in `<data>/xcode-runtimes`. With
none there, the image has Xcode and no simulator runtime, and `wb image
build` says so. `wb-hostd` never runs `xcodebuild` on the host.

Each entry in the folder is opened with `O_NOFOLLOW`, and the build
refuses:

- an entry that isn't a regular file (`xcode-runtime-not-regular`);
- a name without the form of an exported runtime image, which I624
  reports (`xcode-runtime-name`);
- more runtime images than a count cap (`xcode-runtime-too-many`);
- runtime images over a total size cap (`xcode-runtime-too-large`).

The caps are in code.

### The checks on the host

Before it copies anything, the shim of "The Xcode disk" below checks,
and refuses with the rule:

- that Apple signed the app and that it is unchanged
  (`xcode-signature-invalid`), with `codesign --verify --deep --strict`
  against this code requirement:

  ```text
  anchor apple generic and identifier "com.apple.dt.Xcode" and
  (certificate leaf[field.1.2.840.113635.100.6.1.9] or
   (certificate 1[field.1.2.840.113635.100.6.2.6] and
    certificate leaf[field.1.2.840.113635.100.6.1.13] and
    certificate leaf[subject.OU] = "59GAB85EFG"))
  ```

  The first branch is Apple's App Store signature, and the second its
  Developer ID with Apple's own team. `anchor apple generic` without
  the leaf or team clause is never accepted, because every certificate
  Apple issues, any developer's Developer ID included, meets it;
- that the host's user accepted Xcode's license for this Xcode build,
  from the host's own record of it (`xcode-license-not-accepted`);
- that Xcode's minimum macOS version is at most the parent image's
  macOS version (`xcode-too-new-for-guest`);
- that Apple signed each runtime image. A runtime image whose signature
  the shim can't check is refused (`xcode-runtime-signature-invalid`);
- that `<data>` has room for the disk and the image
  (SEC13-bounded-resources, `xcode-disk-no-space`).

The shim reads the license record. Its path becomes a read rule of the
Xcode entry once I624 names it. I624 also measures the code requirement
on an App Store install and on one from Apple's developer site, and
which signature check holds for a runtime image. The build refuses
until each is known.

### The Xcode disk

The Xcode disk is a disk image the host builds. It isn't a shared
directory (SEC02-no-host-fs-share).

`wb-diskfmt` builds it, started through a third fixed `wb-launcher`
entry, `wb-diskfmt-xcode`, whose `WB_DISKFMT_SLOT` is `xcode`
(S04-04-vm-processes, "`wb-diskfmt`"). Only `wb-hostd` starts it,
during an image build. As for the data disk entries, its instance cap
is one, and its shim is started as the leader of a new process group.

The entry's profile parameters are the build bundle,
`<data>/images/macos-build.noindex`, the real path of the Xcode app and
`<data>/xcode-runtimes`. None is a path from the request. For the
`xcode` slot, the slot's bundle of `diskfmt-slot-bundle-mismatch` is
the build bundle.

The profile is `wb-diskfmt`'s (S04-04-vm-processes, "`wb-diskfmt`'s
profile"), with `xcode.img` and its temporary name in the build bundle
in place of the data disk's names, plus:

- reading the Xcode app and the runtime images, and the license record
  ("The checks on the host"), and no other file outside the build
  bundle;
- writing under the mount point `xcode-mnt` in the build bundle;
- starting `/usr/bin/ditto` and `/usr/bin/codesign`.

As for a data disk (S06-13-data-disk-format, "The shim"), the shim:

1. creates `xcode.img` in the build bundle under a temporary name,
   with the same identity checks. Its size is that of the checked
   inputs plus slack, within a bound in code;
2. attaches it, formats it APFS with the volume name `wb-xcode`, and
   mounts the new, empty volume once with `diskutil mount -mountPoint`
   on `xcode-mnt`, a directory it creates in the build bundle. Before
   any write, `statfs` on `xcode-mnt` has to give the device it
   attached as `f_mntfromname` and that path as `f_mntonname`
   (`xcode-mount-mismatch`);
3. copies the app to `Xcode.app` on the volume with
   `ditto --noextattr --noqtn --noacl`, leaving out
   `Contents/_MASReceipt`, and each runtime image to `Runtimes/` on it.
   An App Store install's extended attributes and receipt name the
   host's purchase and device, and the copy leaves them behind. I624
   checks that Xcode still runs without them;
4. checks the signature of the copy again, so a change to the source
   during the copy fails the build;
5. unmounts and detaches the volume, and renames the file to
   `xcode.img`.

Each `ditto` and `codesign` run has a deadline in code, as each
`diskutil` step has. The kill of S06-13-data-disk-format, "When
`wb-diskfmt` doesn't exit", applies to the shim as to the data disk
entries, and `wb-hostd`'s wait covers the sum of its deadlines. I624
measures the copy and check times, and the profile.

The step 2 mount is SEC15-no-guest-disk-mount's one exception, as for a
data disk (S06-05-disks, "Formatting a data disk"): a blank image the
host itself created, before any guest has run with it. The shim also
fills the volume, as the bundle build fills the guest tools disk. The
host never mounts or opens the file again once a guest had it, and it
is deleted with the build bundle.

### What the build records

The shim reports Xcode's version and build and the SHA-256 of each
runtime image to `wb-hostd`, which treats them as untrusted, as it
treats every answer of the shim. `state.db` records them for the image.

`wb-hostd` never reads Xcode, the runtimes or the license record
itself. Before it decides between the layer-only path of
S06-01-image-layers, "Only the layer", and a full build, it starts the
shim in a check-only run, which makes the checks above, reports the
same values, and builds no disk. The layer-only path requires them to
equal the recorded ones. A new Xcode on the host changes nothing until
the next `wb image build`. That build then builds every layer.

### In the build VM

The Xcode step boots its clone in `wb-vmd-layer`, with no network
device, cloned as for the Claude Code layer (S06-01-image-layers). Its
device set adds the Xcode disk, read-only, and only for this step
(S06-06-devices-and-warm-start, "Devices"). `wb-vmd-layer` attaches
`xcode.img` from the build bundle only when the start call sets its
Xcode flag. The call never names a path. `wb-vmd` refuses the flag in
any other slot or entry, and logs the rule (S04-02-vm-provider, "The
device set is fixed and checked").

A named `wb-guestd` operation, `install-xcode`, then runs as root
(S06-02-image-build, "No guest disk mounts"):

1. It checks the volume at `/Volumes/wb-xcode` as it checks the guest
   tools disk: APFS, read-only, and mounted by root
   (S06-12-guest-agent, "Update at start").
2. It copies `Xcode.app` to `/Applications/Xcode.app` with
   `ditto --noextattr --noqtn --noacl`, with owner `root:wheel`, and
   no write permission for group or others. The sealing scan refuses
   files outside `/Users` that a non-system user owns
   (S06-02-image-build, "Sealing").
3. It checks the copy's signature with the same requirement.
4. It runs `xcode-select -s`, `xcodebuild -license accept` and
   `xcodebuild -runFirstLaunch`.
5. It imports each runtime with `xcodebuild -importPlatform`.
6. It unmounts the volume.

A failure fails the build. The host bounds and escapes the failure
text as for terminfo (S06-02-image-build, "Terminfo"). Then the image
is sealed as any other, and the sealing scan applies unchanged.

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
