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
- starting `/usr/bin/ditto` and `/usr/bin/codesign`, with what those
  two need: the metadata of the build bundle's parent directories,
  which `ditto` walks, and the trust services, user database lookup
  and system settings that `codesign` reads to check a signature.

The names are in code. They come from a confined run of the steps
below (X06-guest-xcode, "Follow-up (I624)").

### The steps

As for a data disk (S06-13-data-disk-format, "The shim"), the shim:

1. creates `xcode.img` in the build bundle under a temporary name,
   with the same identity checks. Its size is the sum of the inputs'
   file sizes (`st_size`) plus slack, within a bound in code. The
   copy drops APFS file compression, so the blocks the source takes
   on the host are too few: an Xcode of 4.0 GB on the host took 10 GB
   on the volume;
2. attaches it and formats it APFS with the volume name `wb-xcode`.
   `diskutil eraseDisk` mounts the new, empty volume at
   `/Volumes/wb-xcode`. The shim unmounts it there and mounts it with
   `diskutil mount -mountPoint` on `xcode-mnt`, a directory it creates
   new in the build bundle. An existing `xcode-mnt` refuses the build
   (`xcode-mnt-exists`). Before any write, `statfs` on `xcode-mnt` has
   to give the device it attached as `f_mntfromname` and that path as
   `f_mntonname` (`xcode-mount-mismatch`);
3. copies the app to `Xcode.app` on the volume, and each exported
   runtime to `Runtimes/`, with
   `ditto --norsrc --noextattr --noacl --noqtn`, and removes `Contents/_MASReceipt` from the copy. An App
   Store install's extended attributes and receipt name the host's
   purchase and device. The copy without them still meets the code
   requirement, and Gatekeeper in the guest accepts it;
4. checks the signature of the copy again. That catches a change to
   the source during the copy, and a short copy: `ditto` exited 0
   after a file it failed to write;
5. unmounts and detaches the volume, and renames the file to
   `xcode.img`.

The volume ignores ownership on the host, and in the guest its files
belong to user 99 (S06-14-xcode-image, "In the build VM").

Each `ditto` and `codesign` run has a deadline in code, as each
`diskutil` step has, set above the times X06-guest-xcode, "Follow-up
(I624)", measured. The kill of S06-13-data-disk-format, "When
`wb-diskfmt` doesn't exit", applies to the shim as to the data disk
entries, and `wb-hostd`'s wait covers the sum of its deadlines.

The step 2 mount is SEC15-no-guest-disk-mount's one exception, as for a
data disk (S06-05-disks, "Formatting a data disk"): a blank image the
host itself created, before any guest has run with it. The shim also
fills the volume, as the bundle build fills the guest tools disk. The
host never mounts or opens the file again once a guest had it, and it
is deleted with the build bundle. That delete doesn't follow links, and
stops at a directory whose `st_dev` isn't the bundle's. A volume left
mounted there refuses the build with `xcode-mount-left`, which names
the mount point.

### What the build records

The shim reports Xcode's version and build and the SHA-256 of each
runtime's disk image to `wb-hostd`, which treats them as untrusted, as it
treats every answer of the shim. `state.db` records them for the image.

`wb-hostd` never reads Xcode, the runtimes or the license record
itself. Before it decides between the layer-only path of
S06-01-image-layers, "Only the layer", and a full build, it starts the
shim in a check-only run, which makes the checks of
S06-14-xcode-image, reports the same values, and builds no disk. That
run also gets a new, empty build bundle. The layer-only path requires
them to equal the recorded ones. A new Xcode on the host changes
nothing until the next `wb image build`. That build then builds every
layer.

## Decisions

### A page of its own

- **Why:** the measurements of I624 made S06-14-xcode-image longer
  than a spec page may be. The host's part of the build is one piece
  with its own profile.
- **Decided:** I624.

**Status:** Draft
