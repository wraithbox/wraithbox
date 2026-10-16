# S06-05 - Disks

**Purpose:** A VM's system disk and data disk: how the data disk keeps
what the guest flushes, how the host formats it, and how the guest
mounts it.

## Design

### Disks

A VM has two writable disks:

- a system disk, a clone of the image;
- a data disk, which holds the project users' homes and workspaces.
  Those include Claude Code state (FR13-claude-state) and commits not
  yet pushed.

The data disk is a raw sparse image file. It is attached with
automatic host caching and full synchronization. On macOS that is
`VZDiskImageStorageDeviceAttachment` with
`VZDiskImageCachingModeAutomatic` and
`VZDiskImageSynchronizationModeFull`. So every write the guest flushes
reaches permanent storage on the host.

A raw image gives the host back the space of data the guest deletes.
An ASIF image does not (X05-fs-benchmark).

The space a data disk takes on the host comes from the file's
`st_blocks`, never from an image tool (SEC15-no-guest-disk-mount).
That space counts for SEC13-bounded-resources, and for the free-space
check of S09-policy-credentials-audit.

Each VM bundle is a directory whose name ends in `.noindex`. So
Spotlight doesn't index the disks in it.

With these settings, `npm ci` on the data disk ran at about 71% of the
host on a loaded host. That is under the 80% goal of NFR02-fs-speed.
The goal is not a release gate.

### Bundle files

A VM's third disk is the read-only guest tools disk
(S06-12-guest-agent, "Guest tools disk"). A VM bundle holds these
files, each at its fixed name:

| File | What it holds |
|---|---|
| `hw.bin` | the hardware model |
| `mid.bin` | the machine identifier |
| `aux.wbstore` | the auxiliary storage |
| `sys.wbdisk` | the system disk |
| `data.wbdisk` | the data disk |
| `tools.img` | the copy of the guest tools disk |

A spike puts an APFS clone of its own bundle at the bundle path. A
spike bundle names its guest-written files `aux.img`, `sys.img` and,
when it has a data disk, `data.img` ("Names on the host" below). Whoever
makes the clone renames those files in the clone to the names above,
before `wb-vmd` opens it:

```sh
cd <data>/vms/macos-work.noindex
mv aux.img aux.wbstore
mv sys.img sys.wbdisk
mv data.img data.wbdisk   # only when the spike bundle has a data disk
```

Each file of an APFS clone is a separate file, so the rename doesn't
touch the spike bundle. A clone with the spike's names is refused with
`bundle-file-missing`.

Before it opens any of them, `wb-vmd` checks each one
(S04-02-vm-provider, "The device set is fixed and checked"):

- a missing file is refused with `bundle-file-missing`;
- a file that isn't a regular file with one link, such as a symbolic
  link, or whose real path isn't inside the bundle's real path, is
  refused with `device-set-file-outside-bundle`.

Any other file in the bundle is ignored. Device configuration never
comes from the bundle.

#### Names on the host

The system disk, the auxiliary storage, the data disk and the saved
state are guest-written once the guest has run. A file in the bundle
that a guest writes has no extension that Launch Services opens as a
disk image, no `.img` or `.dmg`, so a double click doesn't make the
host attach a guest-written image (SEC15-no-guest-disk-mount).

The guest tools disk keeps `.img`. The host writes it, and the guest
gets it read-only (S06-12-guest-agent, "Guest tools disk").

### Durability

Claude Code state and commits not yet pushed on the data disk are kept
through a host crash or power loss. Tool caches may be lost. Caches
share the one data disk, because full synchronization costs the
NFR02-fs-speed workloads nothing measurable.

That full synchronization keeps every write the guest flushed through a
host crash rests on Apple's headers. X05-fs-benchmark killed only the
VM's processes, and lost nothing the guest had flushed. So the target
holds for what the guest flushes.

- With its macOS defaults, git in the guest doesn't flush a commit
  (`core.fsyncMethod` is `writeout-only`, `git-config(1)`).
- Whether Claude Code flushes its state isn't measured.
- I231 makes both flush, in V1-M4-claude-end-to-end.

The system disk doesn't need to keep anything through a crash, because
a fresh clone of the image can replace it (S06-03-image-updates, "Updates").

#### After a crash

The host doesn't check or repair a data disk
(SEC15-no-guest-disk-mount). `wb-guestd` refuses a data disk that it
can't mount, as it refuses a missing one ("Mounting the data volume"
below). What was on that disk is lost.

`wb-hostd` never deletes a data disk on its own. The user runs the
command that gives the VM a new data disk (I229). It deletes the old
file, and has `wb-diskfmt` make a new one.

### Formatting a data disk

The host formats a new data disk before any guest has run with it.

- A volume the guest formats APFS is encrypted at rest. A fresh system
  disk with fresh auxiliary storage couldn't unwrap its key, so it
  doesn't mount after a swap.
- A volume the host makes on the image isn't encrypted at rest.

I228 measures three things about a host-made volume: that it stays
unencrypted, that it gives the space the guest frees back to the host,
and that a confined `diskutil` can format it.

#### The shim

The formatting runs in `wb-diskfmt`, a shim. `wb-hostd` starts it
through the fixed `wb-launcher` entry of the VM's slot,
`wb-diskfmt-work` or `wb-diskfmt-isolated`. The entry's profile
parameter is that VM's bundle (S04-architecture). The shim never takes
a path from the request. It confines itself to that bundle, and then:

1. It creates the image under a temporary name in the bundle, with
   `O_CREAT|O_EXCL` and mode 0600. It makes the image sparse at the
   data disk's size, and keeps that descriptor open.
2. It checks the file's identity. `lstat` on the temporary name and
   `fstat` on the descriptor must give the same `st_dev` and `st_ino`,
   with `st_nlink` 1 and `st_blocks` 0.
3. It attaches the file with `diskutil image attach --noMount`. It
   checks that the backing image path of the device it got is the
   temporary name.
4. Right before it runs `diskutil eraseDisk APFS` on that device, it
   repeats the identity check and the backing image path check.
5. The format mounts the new, empty volume on the host once. It is a
   volume the host just made, not one a guest wrote.
6. `wb-diskfmt` reports the volume's UUID, unmounts the volume, and
   detaches the image.
7. `wb-hostd` records the volume UUID and the temporary name in
   `state.db`.
8. The shim renames the file to its final name with `renamex_np` and
   `RENAME_EXCL`. That fails with the rule when a file has that name.
9. `wb-hostd` records the disk as formatted.

When a check fails, the shim fails with the rule. It detaches only a
device it attached.

#### The temporary and final names

- The host never attaches a file at the final name.
- A leftover temporary file is deleted, never attached.
- A file can have its final name while `state.db` doesn't record it as
  formatted, as after a crash between the rename and the record.
  `wb-hostd` then refuses to start the VM with the rule. The refusal
  names the command that gives the VM a new data disk (I229).
  `wb-hostd` never deletes the file.
- The final name has no extension that Launch Services opens as a disk
  image ("Names on the host" above). So a double click doesn't mount
  it.

A data disk is unencrypted at rest, except by the host's own
FileVault. A user who mounts it by hand gives the host kernel a
guest-written filesystem.

#### The one exception

Formatting a blank image the host itself created, before any guest has
run with it, is the one exception SEC15-no-guest-disk-mount makes. The
steps above keep it to that file:

- the shim attaches only the file it just created under its temporary
  name;
- a VM starts only with a disk that `state.db` records as formatted.

The exception ends when the VM first starts with the disk.

The VM's system disk, auxiliary storage and saved state are
guest-written too. The Virtualization API in `wb-vmd` is the one host
code that opens them.

### Mounting the data volume

`wb-guestd` mounts the data volume after first contact with
`wb-hostd`, when the volume UUID arrives. It does that before it starts
anything as a project user. The mount point is a fixed path outside
`/Volumes`, which holds the homes.

#### The mount

1. `wb-guestd` waits up to 30 s for the volume with the UUID that the
   host recorded at format time. It identifies the volume only by that
   UUID.
2. By then Disk Arbitration has mounted it at `/Volumes/<name>`,
   without `nosuid`. So `wb-guestd` unmounts it.
3. It mounts it at the home path, with `nobrowse`, `nosuid`, `nodev`
   and ownership on.
4. It checks that `diskutil` reports the volume "Encrypted: No".

An encrypted or reformatted volume, or a missing one, is refused with
the rule. The refusal names the command that gives the VM a new data
disk (I229, NFR06-explained-refusals).

The mount isn't repeated while the guest runs. Once a process has run
as a user whose home is under the mount point, macOS refuses another
mount there until the next boot.

#### Before every session start

`wb-guestd` reads the mount's flags again with `statfs`. Guest root can
change them in place with `mount -u`. It gains nothing by that, because
guest root is already root.

No session starts unless that exact volume is mounted at the home path
with every flag. The refusal is logged with the mount found and the
rule (SEC08-proj-isolation). Without `nosuid`, a setuid-root file that
guest root left in a home gives a project user root, also after a
swap.

#### Owners and modes

`wb-guestd` checks with `lstat`, never following a link:

- that the mount root is a real directory owned by `root:wheel`, with
  mode 755;
- that each home is a real directory owned by its user's kept UID and
  GID, with mode 700.

A home that fails is refused to its user, with the rule logged. The
other users go on.

A new home is made with `mkdir` on a path that doesn't exist yet. It
is never made by `createhomedir` into an existing path. It is then
given to its user with `fchown` on the descriptor of the new
directory, or `lchown`. So a link put in its place is never followed.

When it creates a user, `wb-guestd` refuses a UID or GID that owns any
top-level entry on the volume other than that user's own home. It logs
the rule.

`wb-guestd` keeps session records in a root-owned directory on the data
disk (S06-09-session-lifecycle). That directory is checked the same
way. Its contents are parsed as untrusted input, because guest root may
have written them.

## Decisions

### Full synchronization, not the relaxed modes

- **Why:** X05-fs-benchmark found the relaxed modes don't speed up the
  NFR02-fs-speed workloads. Apple's header says an image without
  synchronization "cannot safely be reused" after a host crash or
  power loss.

### What survives a crash

Claude Code state and commits not yet pushed are kept. Tool caches may
be lost. A data disk the guest can't mount after a crash is lost.

- **Decided:** I44.

### A host-formatted data disk on an image file

- **Rejected:** a volume the guest formats, because it doesn't mount
  after a swap.
- **Rejected:** a USB data disk. Without a grant, TCC denies every
  guest process access to it (X08-data-disk).

### Bundle names that a double click doesn't mount

- **Rejected:** the spike bundles' `sys.img` and `aux.img`, which let a
  spike bundle work without a rename. A double click on either one
  attaches a guest-written image on the host.
- **Decided:** I312.

**Status:** Draft
