# S06-05 - Disks

**Purpose:** A VM's system disk and data disk: how the data disk keeps what the
guest flushes, how the host formats it, and how the guest mounts it.

## Design

### Disks

A system disk (clone of the image) and a data disk holding
project users' homes and workspaces, including Claude Code state
(FR13-claude-state) and commits not yet pushed. The data disk is a
raw sparse image file, attached with automatic host caching and full
synchronization (on macOS, `VZDiskImageStorageDeviceAttachment` with
`VZDiskImageCachingModeAutomatic` and
`VZDiskImageSynchronizationModeFull`), so every write the guest
flushes reaches permanent storage on the host. The relaxed modes are
not used: X05-fs-benchmark found they don't speed up the
NFR02-fs-speed workloads, and Apple's header says an image without
synchronization "cannot safely be reused" after a host crash or
power loss. A raw image gives the host back the space of data the
guest deletes, and an ASIF image does not (X05-fs-benchmark). The
space a data disk takes on the host, for SEC13-bounded-resources and
the free-space check of S09-policy-credentials-audit, comes from the
file's `st_blocks`, never from an image tool
(SEC15-no-guest-disk-mount). Each VM bundle is a directory whose name
ends in `.noindex`, so Spotlight doesn't index the disks in it.
With these settings, `npm ci` on the data disk ran at about 71% of
the host on a loaded host, under the 80% goal of NFR02-fs-speed. The
goal is not a release gate.

### Durability

Claude Code state and commits not yet pushed on the data disk are
kept through a host crash or power loss, and tool caches may be lost
(decided on I44). Caches share the one data disk, because full
synchronization costs the NFR02-fs-speed workloads nothing
measurable. That it keeps every write the guest flushed through a
host crash rests on Apple's headers. X05-fs-benchmark killed only the
VM's processes, and lost nothing the guest had flushed. So the target
holds for what the guest flushes. With its macOS defaults, git in the
guest doesn't flush a commit (`core.fsyncMethod` is `writeout-only`,
`git-config(1)`), and whether Claude Code flushes its state isn't
measured. I231 makes both flush, in V1-M2-walking-skeleton. The
system disk doesn't need to keep anything through a crash, because a
fresh clone of the image can replace it (S06-03-image-updates, "Updates").

After a crash, the host doesn't check or repair a data disk
(SEC15-no-guest-disk-mount). `wb-guestd` refuses a data disk that it
can't mount, as it refuses a missing one (below). What was on that
disk is lost, a loss decided on I44. `wb-hostd` never deletes a data
disk on its own. The command that gives the VM a new data disk
(I229), which the user runs, deletes the old file and has
`wb-diskfmt` make a new one.

### Formatting a data disk

The host formats a new data disk before any guest has run with it.
A volume the guest formats APFS is encrypted at rest, and a fresh
system disk with fresh auxiliary storage couldn't unwrap its key, so
it doesn't mount after a swap. A volume the host makes on the image
isn't encrypted at rest. A USB data disk is not an option: without a
grant, TCC denies every guest process access to it (X08-data-disk).
I228 measures that a host-made volume stays unencrypted, gives the
space the guest frees back to the host, and can be formatted by a
confined `diskutil`.
The formatting runs in `wb-diskfmt`, a shim that `wb-hostd` starts
through the fixed `wb-launcher` entry of the VM's slot
(`wb-diskfmt-work` or `wb-diskfmt-isolated`), whose profile parameter
is that VM's bundle (S04-architecture). It never takes a path from
the request. It confines itself to that bundle, and then:
1. creates the image under a temporary name in the bundle with
   `O_CREAT|O_EXCL`, mode 0600, makes it sparse at the data disk's
   size, and keeps that descriptor open;
2. checks the file's identity: `lstat` on the temporary name and
   `fstat` on the descriptor give the same `st_dev` and `st_ino`, with
   `st_nlink` 1 and `st_blocks` 0. It then attaches the file with
   `diskutil image attach --noMount`, and checks that the backing
   image path of the device it got is the temporary name. When a check
   fails, the shim fails with the rule, and detaches only a device it
   attached;
3. repeats the identity check and the backing image path check
   immediately before it runs `diskutil eraseDisk APFS` on that
   device, and fails the same way on a mismatch. The format mounts the
   new, empty volume on the host once. It is a volume the host just
   made, not one a guest wrote. `wb-diskfmt` reports the volume's
   UUID, unmounts the volume, and detaches the image;
4. `wb-hostd` records the volume UUID and the temporary name in
   `state.db`. The shim then renames the file to its final name with
   `renamex_np` and `RENAME_EXCL`, which fails with the rule when a
   file has that name, and `wb-hostd` records the disk as formatted.

The host never attaches a file at the final name. A leftover
temporary file is deleted, never attached. When a file has its final
name but `state.db` doesn't record it as formatted, as after a crash
between the rename and the record, `wb-hostd` refuses to start the
VM with the rule, names the command that gives the VM a new data disk
(I229), and never deletes the file. The final name has no extension
that Launch Services opens as a disk image (no `.img` or `.dmg`), so
a double click doesn't mount it. A data disk is unencrypted at rest
except by the host's own FileVault, and a user who mounts it by hand
gives the host kernel a guest-written filesystem. Formatting a blank
image the host itself created, before any guest has run with it, is
the one exception SEC15-no-guest-disk-mount makes. The steps above
keep it to that file: the shim attaches only the file it just created
under its temporary name, and a VM starts only with a disk that
`state.db` records as formatted. The exception ends when the VM first
starts with the disk. The VM's saved state and auxiliary storage are
guest-written too. The Virtualization API in `wb-vmd` is the one host
code that opens them.

### Mounting the data volume

`wb-guestd` mounts the data volume after first contact with
`wb-hostd`, when the volume UUID arrives, and before it starts
anything as a project user, at a fixed path outside `/Volumes` that
holds the homes:
- It waits up to 30 s for the volume with the UUID the host recorded
  at format time, and identifies it only by that UUID. Disk
  Arbitration has mounted it at `/Volumes/<name>` without `nosuid` by
  then, so `wb-guestd` unmounts it and mounts it at the home path
  with `nobrowse`, `nosuid`, `nodev` and ownership on. The mount isn't
  repeated while the guest runs: once a process has run as a user
  whose home is under the mount point, macOS refuses another mount
  there until the next boot.
- It checks that `diskutil` reports the volume "Encrypted: No". An
  encrypted or reformatted volume, or a missing one, is refused with
  the rule, and the refusal names the command that gives the VM a
  new data disk (I229, NFR06-explained-refusals).
- Before every session start it reads the mount's flags again with
  `statfs`. Guest root can change them in place with `mount -u`, and
  gains nothing by it, because guest root is already root. No
  session starts unless that exact volume is mounted at the home path
  with every flag. The refusal is logged with the mount found and
  the rule (SEC08-proj-isolation). Without `nosuid`, a setuid-root
  file that guest root left in a home gives a project user root, also
  after a swap.
- It checks with `lstat`, never following a link, that the mount root
  is a real directory owned by `root:wheel` with mode 755, and that
  each home is a real directory owned by its user's kept UID and GID
  with mode 700. A home that fails is refused to its user, with the
  rule logged, and the other users go on. A new home is made with
  `mkdir` on a path that doesn't exist yet, never by `createhomedir`
  into an existing path, and is then given to its user with `fchown`
  on the descriptor of the new directory, or `lchown`, so a link put
  in its place is never followed. When it creates a user,
  `wb-guestd` refuses a UID or GID that owns any top-level entry on
  the volume other than that user's own home, and logs the rule. The
  root-owned directory on the data disk where `wb-guestd` keeps
  session records (S06-09-session-lifecycle) is checked the same way, and
  its contents are parsed as untrusted input, because guest root may
  have written them.

**Status:** Draft
