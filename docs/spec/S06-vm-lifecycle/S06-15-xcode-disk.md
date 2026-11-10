# S06-15 - The Xcode Disk

**Purpose:** How the host builds the read-only disk that holds Xcode
and the simulator runtimes for an image build, under the
`wb-diskfmt` shim's profile, and what the build records.

**Requirements:** FR06-native-tools, SEC02-no-host-fs-share,
SEC12-least-privilege, SEC13-bounded-resources,
SEC15-no-guest-disk-mount

**Tests:** C11-host-daemons

## Design

### The entry

The Xcode disk is a disk image the host builds. It isn't a shared
directory (SEC02-no-host-fs-share). The checks it starts with, and
what the guest does with it, are in S06-14-xcode-image.

`wb-diskfmt` builds it, started through a third fixed `wb-launcher`
entry, `wb-diskfmt-xcode`, whose `WB_DISKFMT_SLOT` is `xcode`
(S04-04-vm-processes, "`wb-diskfmt`"). Only `wb-hostd` starts it,
during an image build. `wb-hostd` clones the parent into the new,
empty build bundle, then starts `wb-diskfmt-xcode`, and starts no
`wb-vmd` in that bundle until `wb-launcher` has reported the shim's
exit. Then it starts `wb-vmd-layer`. As for the data disk entries, its
instance cap is one, and its shim is started as the leader of a new
process group.

The entry's profile parameters are the build bundle,
`<data>/images/macos-build.noindex`, the real path of the Xcode app and
`<data>/xcode-runtimes`. The shim resolves `/Applications/Xcode.app`
with `realpath` itself before it confines itself, and applies the
`/Applications/*.app` rule (S06-14-xcode-image, "Where Xcode comes
from"). The shim takes none of these paths from the request or the
environment. For the `xcode` slot, the slot's bundle of
`diskfmt-slot-bundle-mismatch` is the build bundle.

### The profile

The profile is `wb-diskfmt`'s (S04-04-vm-processes, "`wb-diskfmt`'s
profile"), with `xcode.img` and its temporary name in the build bundle
in place of the data disk's names, plus:

- reading the Xcode app, the exported runtimes and the license record
  (S06-14-xcode-image, "The checks on the host"), and no other file
  outside the build bundle;
- creating the directory `xcode-mnt` in the build bundle, by that
  literal name, writing under it, and mounting and unmounting on it
  (`file-mount`, `file-unmount`);
- starting `/usr/bin/ditto` and `/usr/bin/codesign`, and reading the
  metadata of the build bundle's parent directories, which `ditto`
  walks;
- `mach-lookup` of `com.apple.trustd.agent`, the one service `codesign`
  needs to check a signature.

`codesign` with no Mach service refuses Apple's own signatures with
`CSSMERR_TP_NOT_TRUSTED`. With `com.apple.trustd.agent` alone, the
deep check of Xcode against S06-14-xcode-image's requirement and the
check of a runtime's disk image both pass, and a wrong requirement
still fails. `ditto` doesn't need one (X06-guest-xcode, "Follow-up
(I624)", "The profile"). The profile doesn't allow another service. Not
`com.apple.trustd`, not `com.apple.SecurityServer`, which serves the
keychain, not `com.apple.ocspd`, and not the user database lookup that
the base profile denies.

`com.apple.trustd.agent` runs outside the
sandbox as the host user, so a subverted shim can ask it to evaluate
any certificate, and it may fetch from the network to do so
(S04-04-vm-processes, "`wb-diskfmt`'s profile"). The names are in code,
and the opt-in host test's confined run checks them (I681).

### Finding its volume

The shim knows its volume only by device. `diskutil image attach`
reports the device it attached. The shim's volume is a volume of the
APFS container whose one physical store is that device, read from the
I/O Registry. APFS reports a mounted volume's `f_mntfromname` as a
volume of that synthesized container, never as the attached device.
The shim never looks a volume up by its name or its mount point. It
never takes a leftover volume named `wb-xcode` for its own. Disk
Arbitration then mounts the new one at `/Volumes/wb-xcode 1`.

### The steps

As for a data disk (S06-13-data-disk-format, "The shim"), the shim:

1. creates `xcode.img` in the build bundle under a temporary name,
   with the same identity checks. Its size is the sum of the inputs'
   file sizes (`st_size`) plus slack, within a bound in code. The
   copy drops APFS file compression, so the blocks the source takes
   on the host are too few: an Xcode of 4.0 GB on the host took 10 GB
   on the volume;
2. attaches it and formats it APFS with the volume name `wb-xcode`.
   `diskutil eraseDisk` mounts the new, empty volume in `/Volumes`.
   The shim unmounts that volume by its device, and mounts it with
   `diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint` on
   `xcode-mnt`, a directory it creates new in the build bundle. An
   existing `xcode-mnt` refuses the build (`xcode-mnt-exists`). Before
   any write, `statfs` on `xcode-mnt` has to give that path as
   `f_mntonname` and a volume of its container as `f_mntfromname`
   (`xcode-mount-mismatch`). The volume's root has to be empty, apart
   from a fixed list in code of what macOS makes on a new volume
   (`xcode-volume-not-empty`);
3. copies the app to `Xcode.app` on the volume, and each exported
   runtime to `Runtimes/`, with
   `ditto --norsrc --noextattr --noacl --noqtn`;
4. removes every `_MASReceipt` and every `.DS_Store` from the copied
   app, at any depth. An App Store install's receipt names the host's
   purchase and device. Removing them is also an integrity step:
   `codesign --verify --deep --strict` accepts a planted file in
   either place, and the copy without them still meets the code
   requirement, and Gatekeeper in the guest accepts it;
5. checks the copy, which catches a change to the source during the
   copy, and a short copy, since `ditto` exited 0 after a file it
   failed to write:
   - the app's signature with S06-14-xcode-image's requirement, and
     each runtime's disk image with `identifier image and anchor
     apple`;
   - a walk of the volume with `lstat`, which accepts only `Xcode.app`
     and `Runtimes` at the root and, under `Runtimes`, only the checked
     runtimes in the layout of S06-14-xcode-image, "Where the runtimes
     come from" (`xcode-volume-unexpected`);
   - Xcode's version, build, `LicenseInfo.plist` and minimum macOS
     version, read again from the checked copy, with the license and
     macOS version checks of S06-14-xcode-image made again on them, and
     the SHA-256 of each copied disk image;
6. checks the device and the root again, as in steps 2 and 5, then
   unmounts the volume by its device, detaches it, and renames the
   file to `xcode.img`.

The copy keeps each exported runtime's other files. The guest imports
only the disk image (S06-14-xcode-image, "In the build VM"), but the
import was measured only with the disk image in its directory, beside
its `Firmware` files, and an import without them wasn't tried. The
walk in step 5 holds them to their names and types.

The volume ignores ownership on the host, and in the guest its files
belong to user 99 (S06-14-xcode-image, "In the build VM").

Each `ditto` and `codesign` run has a deadline in code, as each
`diskutil` step has, set above the times X06-guest-xcode, "Follow-up
(I624)", measured. The deep check took 53 to 107 s on the source and
45 to 46 s on the copy. Each runtime's disk image took 22 to 27 s, and
is checked on the source and again on the copy. The kill of S06-13-data-disk-format,
"When `wb-diskfmt` doesn't exit", applies to the shim as to the data
disk entries, and `wb-hostd`'s wait covers the sum of its deadlines.

### The SEC15 exception

The shim mounts the volume twice: once where `eraseDisk` mounts it in
`/Volumes`, which the shim undoes before any write, and once on
`xcode-mnt`, where it fills it. Both are SEC15-no-guest-disk-mount's
one exception, as for a data disk (S06-05-disks, "Formatting a data
disk"): a blank image the host itself created, before any guest has
run with it. The shim fills the volume as the bundle build fills the
guest tools disk.

The host never mounts or opens the file again once a
guest had it, and it is deleted with the build bundle. That delete
doesn't follow links, and stops at a directory whose `st_dev` isn't
the bundle's. A volume left mounted on `xcode-mnt` refuses the build
with `xcode-mount-left`, which names the mount point.

A shim killed between the two mounts leaves its volume in `/Volumes`.
As with a killed data disk shim, `wb-hostd` logs the temporary image,
and the device stays attached until the user ejects it or the host
restarts (S06-13-data-disk-format, "When `wb-diskfmt` doesn't exit").
That volume never reaches a guest, because its file is never renamed
to `xcode.img`, and a later build finds its own volume by device.

### What the build records

The shim reports Xcode's version and build and the SHA-256 of each
runtime's disk image, all read from the checked copy, to `wb-hostd`,
which treats them as untrusted, as it treats every answer of the
shim. `state.db` records them for the image.

`wb-hostd` never reads Xcode, the runtimes or the license record
itself. It decides between the layer-only path of
S06-01-image-layers, "Only the layer", and a full build after a
check-only run of the shim, which makes the checks of
S06-14-xcode-image, reports the same values from the source, and
builds no disk. That run also gets a new, empty build bundle. The
layer-only path requires them to equal the recorded ones. A new Xcode
on the host changes nothing until the next `wb image build`. That
build then builds every layer.

### Open for I681

- Whether `diskutil apfs createContainer` and `diskutil apfs
  addVolume` with `-mountpoint` mount the new volume on `xcode-mnt`
  directly, which would keep it out of `/Volumes` altogether.
- What macOS makes on the root of a new volume mounted with
  `nobrowse`, which is the list `xcode-volume-not-empty` allows.
- Whether the confined shim needs any service besides
  `com.apple.trustd.agent`. Adding one needs a measured failure without
  it, and this page then says what a subverted shim gains from it.

## Decisions

### A page of its own

The host's build of the Xcode disk has its own page, apart from
S06-14-xcode-image.

- **Why:** the measurements of I624 made S06-14-xcode-image longer
  than a spec page may be. The host's part of the build is one piece
  with its own profile and its own launcher entry.
- **Rejected:** shortening S06-14-xcode-image's measured detail to keep
  one page, which would leave the checks without the numbers that set
  their deadlines.
- **Decided:** I624, 2026-11-10.

## Out of scope

- Other local accounts on the Mac as an adversary. The page is written
  against the host user's own account, which T00-index trusts.
- Building the disk on a host other than macOS, which Xcode doesn't
  run on.
- Removing the runtimes that the export leaves installed on the host.

**Status:** Draft
