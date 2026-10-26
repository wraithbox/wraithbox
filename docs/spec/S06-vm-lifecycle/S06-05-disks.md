# S06-05 - Disks

**Purpose:** A VM's system disk and data disk: how the data disk keeps
what the guest flushes, and how the guest mounts it. How the host
formats it is in S06-13-data-disk-format.

## Design

### Disks

A VM has two writable disks:

- a system disk, a clone of the image;
- a data disk, which holds the project users' homes and workspaces.
  Those include Claude Code state (FR13-claude-state) and commits not
  yet pushed.

The data disk is a raw sparse image file. Its nominal size is the most
it can grow to. That size is a setting, read when a disk is made and
bounded in code. A new disk takes almost nothing on the host until the guest
writes to it. It is attached with
automatic host caching and full synchronization. On macOS that is
`VZDiskImageStorageDeviceAttachment` with
`VZDiskImageCachingModeAutomatic` and
`VZDiskImageSynchronizationModeFull`. So every write the guest flushes
reaches permanent storage on the host.

A raw image gives the host back the space of data the guest deletes.
An ASIF image does not (X05-fs-benchmark). On the data volume, mounted
`nobrowse` as "The mount" says, the space comes back when the volume is
unmounted or mounted again, not when the guest deletes. That is at a
clean guest shutdown, or at `wb-guestd`'s mount when the VM next starts
(X08-data-disk, "Follow-up (I228)"). Until then the file's `st_blocks`
still count the deleted data.

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

A spike puts an APFS clone of its own bundle at the bundle path, and
that bundle names its guest-written files `aux.img`, `sys.img` and,
when it has a data disk, `data.img` ("Names on the host" below). Whoever
makes the clone renames those files in the clone to the names above,
before `wb-vmd` opens it:

```sh
cd <data>/vms/macos-<slot>.noindex
mv -n aux.img aux.wbstore
mv -n sys.img sys.wbdisk
mv -n data.img data.wbdisk   # only when the spike bundle has a data disk
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
host attach a guest-written image (SEC15-no-guest-disk-mount). The
saved state has no fixed name in the bundle yet, and the name it gets
follows this rule.

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

`wb-hostd` never deletes a data disk on its own. The user runs `wb vm
reset-data` (S05-03-management-commands). It deletes the old file, and
has `wb-diskfmt` make a new one.

### Formatting a data disk

The host formats each new data disk before any guest has run with it,
in the confined shim `wb-diskfmt`. That is the one exception
SEC15-no-guest-disk-mount makes, and S06-13-data-disk-format has its
steps and the checks before a VM starts with its disk.

### Mounting the data volume

`wb-guestd` mounts the data volume after first contact with
`wb-hostd`, when the volume UUID arrives. `wb-hostd` sends it with
`GuestService.MountData` before any other call, after every accepted
hello of a VM run, the first one included.
`wb-guestd` mounts the volume before it starts anything as a project
user. The mount point is `/private/var/wbhomes`, a fixed path outside
`/Volumes`, which holds the homes.

#### The mount

1. `wb-guestd` waits up to 30 s for the volume with the UUID that the
   host recorded at format time. It identifies the volume only by that
   UUID.
2. It checks that `diskutil` reports the volume unencrypted, both at
   rest and by FileVault. A report that doesn't say counts as
   encrypted.
3. By then Disk Arbitration has mounted it at `/Volumes/<name>`,
   without `nosuid`. So `wb-guestd` unmounts it.
4. It mounts it at the home path with `diskutil mount -mountOptions
   nobrowse,nosuid,nodev,owners`, and runs the check of the next
   section on the mount.

`wb-guestd` gives up on the mount after 40 s in all, and answers with
the refusal. `wb-hostd` waits 45 s for the answer.

An encrypted volume, a missing one, or one that fails the check is
refused with the rule. `wb-hostd` keeps the VM running and the link
up, and refuses every session start in the VM with that rule and the
fix: stop the VM with `wb vm stop`, then `wb vm reset-data`
(NFR06-explained-refusals). `wb vm status` shows the refusal. A
missing data disk record in `state.db` is refused the same way, but
its fix doesn't name `wb vm reset-data`, which deletes every home.

A mount that `wb-guestd` leaves unanswered for 45 s, or a call that
fails with `UNAVAILABLE`, drops the link, and the next link asks
again. Each accepted hello sets the mount back to pending, so no
session starts on a new link until its mount succeeds.

The mount isn't repeated while the guest runs. Once a process has run
as a user whose home is under the mount point, macOS refuses another
mount there until the next boot. So after a reconnect, `wb-guestd`
finds the volume mounted already and only checks it. When the host
still asks for the first mount, it checks the mount, then claims the
root as the next section says, then checks the root.

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

The host formats the volume as the host user, so the guest may see the
mount root with another owner or mode. On a volume's first mount,
`wb-guestd` logs the root's owner and mode as it found them ("data
volume root before its first claim"). It then gives the root to
`root:wheel` with mode 755, by `fchown` and `fchmod` on a descriptor
opened without following a link. `wb-hostd` records the first mount
that succeeded in `state.db` and asks for this only until then. A
reformat of the data disk clears the record.

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

### Bundle names that a double click doesn't mount

- **Rejected:** the spike bundles' `sys.img` and `aux.img`, which let a
  spike bundle work without a rename. A double click on either one
  attaches a guest-written image on the host.
- **Decided:** I312.

**Status:** Draft
