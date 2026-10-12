# X08 - Data disk for homes

**Purpose:** Whether guest user homes can live on a second disk and
survive a replacement of the system disk, and what the host must keep
to recreate the users.

Brief: B22-data-disk

## For review

- **Decides:** the answer to X08-data-disk (I22), how the data disk
  gets its filesystem, and which process formats it, which
  S06-vm-lifecycle, "Disks", now states, with the steps of "Updates"
  that recreate the project users.
- **You are approving:** the answer "yes, with conditions", and the
  change to how a data disk is made. A volume the guest formats APFS on
  its data disk is encrypted at rest, and a fresh system disk with fresh
  auxiliary storage couldn't unwrap its key. Instead, the host formats
  each new data disk APFS before any guest has booted with it, which
  gives a volume without that key. `diskutil` mounts that new, empty
  volume on the host once while formatting, and the host never attaches
  the disk again. The users are recreated from host state with the same
  UID, GID and `GeneratedUID`.
- **Controls touched:** SEC08-proj-isolation is kept on these
  conditions, which hold for both options of decision 1:
  - `wb-guestd` mounts the volume `nosuid` and `nodev` with ownership
    on, and checks the flags before every session, because a
    setuid-root file planted there gives a project user root after a
    swap;
  - a UID, GID or `GeneratedUID` is never reused for any project on
    any data disk of this host;
  - `wb-guestd` checks the homes with `lstat` and never follows a link
    in them with root's rights.

  SEC11-root-gains-nothing: a guest root that wrote to the data disk
  keeps what it wrote after a swap, because the swap replaces only the
  system disk. Formatting a blank image that no guest has booted with
  is outside the scope of the I44 rule that the host never mounts a
  disk after a guest has booted from it, given the steps of
  S06-vm-lifecycle, "Disks", that make sure the image is that blank
  file. SEC12-least-privilege: decision 2, below.
- **Assumed:** that a guest never turns encryption on for a volume it
  didn't format, which held for four boots here, and that the space a
  guest frees on the host-made volume goes back to the host as on a
  guest-made one (X05-fs-benchmark measured the guest-made one). I228
  measures both. That the key of a guest-made volume needs the old
  system disk is inferred from the kernel log: the clones also replaced
  the auxiliary storage, and the spike didn't separate the two.
- **Open decisions:**
  1. Who formats the data disk. Recommended: the host, APFS, before the
     first boot, as above. The alternative is the guest formatting it
     HFS+ (Mac OS Extended), which the spike showed is kept across the
     swap as well, and which keeps the host out of formatting. HFS+ has
     no clones and no snapshots, and X05-fs-benchmark didn't measure it.
  2. Which host process runs `diskutil`. `wb-hostd` can't: under its
     self-sandbox it starts only fixed `wb-launcher` entries, with no
     arguments (S04-architecture). Recommended: a fixed `wb-launcher`
     entry for a small shim, `wb-diskfmt`, that confines itself to one
     VM bundle, takes the path from configuration, never from the
     request, and runs the steps of "Disks". S06-vm-lifecycle is
     written for it. If it is chosen, the process and launcher tables
     of S04-architecture change in a follow-up, which I218 (one
     `wb-vmd` per VM, its launcher entries) also edits.
- **Brief:** B22-data-disk

## Question

From X00-index: can guest user homes live on a second disk and survive
a replacement of the system disk? The issue adds: mount a second APFS
volume at boot from a root daemon, create users with homes on it, fill
them, replace the system disk with a fresh clone of the image, recreate
the users with the same UID and GID from host state, and check
ownership, permissions, the login keychain, Claude Code state, and that
one project user can't read another's home.

## Answer

**Yes, with conditions.** Homes on a data disk survive the swap, and
recreated users find them unchanged, but not with the data disk S06
described. Each part, with the evidence in "Measurements":

1. **A guest-formatted APFS volume doesn't survive the swap: no.** On
   Apple Silicon, every APFS volume on an internal disk is encrypted at
   rest. The guest sees the virtio data disk as internal, and
   `diskutil` reported the new volume "FileVault: No (Encrypted at
   rest)". On a fresh clone of the image, which replaced both the
   system disk and the auxiliary storage, Disk Arbitration failed to
   mount it at boot, and by hand the kernel logged `AppleVPKeyStore:
   volume ... unlock failed (-25)` and `failed to unwrap volume key`.
   The system disk and auxiliary storage it was made with still
   mounted it at the next boot. So the key needs one of the two, or
   both. The spike didn't separate them.
2. **A passphrase volume doesn't survive it either: no.** An APFS
   volume added with `diskutil apfs addVolume -passphrase` failed to
   unlock with its passphrase on another system disk and auxiliary
   storage (g2, which had never unlocked it)
   (`AppleVPKeyStore: xart error: -3`, `failed to decode vek`). So the
   key needs more than the passphrase. The log points at xART, the
   anti-replay store, which is on the system disk. That points at the
   system disk, but is inferred: the auxiliary storage changed too.
3. **A USB data disk can't be used: no.** Attached as USB mass storage,
   the disk is external to the guest, and its APFS volume isn't
   encrypted. Without a grant, TCC refuses every process in the guest
   access to it
   (`kTCCServiceSystemPolicyRemovableVolumes`), root included, and a
   job launchd starts directly. The spike didn't try a grant, which on
   macOS comes from MDM or from a person at the guest's screen.
4. **A host-formatted APFS volume survives: yes.** The host creates a
   sparse raw file, attaches it as the user without mounting
   (`hdiutil attach -nomount`, as in X17-image-build, which prints that
   it is deprecated for `diskutil image attach --noMount`), and runs
   `diskutil eraseDisk APFS`. That mounts the new, empty volume on the
   host once, at `/Volumes/<name>`. Then it detaches the image, which
   unmounts it. The spike's script checked the device by its protocol
   ("Disk Image") and size before formatting; S06-vm-lifecycle checks
   the backing image path instead. On the host, the image
   is not internal storage, so the volume isn't encrypted at rest. The
   guest sees it as internal, "Encrypted: No", owners enabled, and
   Disk Arbitration mounts it at boot on every system disk tried. It
   was still unencrypted after the guest wrote to it.
5. **A guest-formatted HFS+ volume survives: yes.** HFS+ has no
   encryption at rest, and it mounted the same way on every system
   disk.
6. **Recreated with the same IDs, the homes are unchanged: yes.** On a
   fresh system disk, the users were recreated from host state (name,
   UID, GID, user and group `GeneratedUID`, home path) without touching
   the homes. A snapshot of every file (numeric owner, group, mode,
   flags, size, ACL, xattrs, checksum) was identical before and after,
   on both volumes. As each user: Claude Code settings,
   `~/.claude.json` and a conversation read through the session's
   symbolic link; a write in the home worked; a file mode 400, a file
   flagged `uchg`, and a file whose ACL denies the user write still
   refused; a keychain file in the home unlocked with its password,
   returned its item and took a new one.
7. **The login keychain: none.** A project user without a GUI login has
   no login keychain (`security login-keychain` returns the System
   keychain). A keychain file in the home is an ordinary file and
   survives with the home.
8. **One project user can't read another's home: yes, before and
   after.** Homes are mode 700. The other user got "Permission denied"
   listing the home, reading `~/.claude.json` and the keychain, and
   running a binary in it, and couldn't write in the volume's root. An
   existing account with another UID couldn't list the homes before the
   users existed on the new system disk.
9. **A new `GeneratedUID` breaks ACLs: verified.** Recreated with the
   same UID and GID but new `GeneratedUID`s on g1, a system disk that
   had never had these users (not a fresh clone: it had the users of
   the earlier cycles), owners and modes still
   matched, and every check passed except one: the ACL entries naming
   the user or its group now show the old UUID unresolved, and the file
   whose ACL denied the user write took the write. A deny entry fails
   open, and an allow entry stops granting.
10. **`nosuid` is needed: verified.** A setuid-root probe that guest
    root put in a home gave the project user effective uid 0 when the
    volume was mounted without `nosuid`, both before the swap (the
    guest APFS volume, boot 1) and after it (the HFS+ volume, boot 7). With `nosuid` it ran with the user's own
    uid. A copy of `/usr/bin/id` doesn't work as a probe: macOS kills
    it outside the system volume, setuid or not.
11. **The user record takes the `GeneratedUID` only first: verified.**
    Through `dscl`, opendirectoryd refuses (`eDSPermissionError`) to
    change `GeneratedUID`, and to change or delete `NFSHomeDirectory`,
    once the record has `UniqueID` and `PrimaryGroupID`. It also refuses to
    delete the record (I138). Setting `GeneratedUID` as the first
    attribute of the new record works. So a home path is set once, at
    creation, and a wrong one can't be fixed.
12. **The mount point is outside `/Volumes`, and the mount comes before
    any project user: verified.** Disk Arbitration mounts the volume at
    boot at `/Volumes/<name>`, without `nosuid`. `wb-guestd` unmounts it
    and mounts it at the fixed home path with `nobrowse,nosuid,nodev,owners`
    in 0.40 to 0.60 s. `/Volumes` belongs to Disk Arbitration, which
    removed a mount point there when the volume was unmounted. Once any
    process had run as a user whose home is under the mount point, the
    kernel's System Policy denied another mount on it
    (`deny(1) file-mount`) until the next boot, and root couldn't
    remove the directory. On a fresh boot, before any project user
    process, the mount always worked.
13. **`/etc/fstab` is not a way in: verified.** The root daemon couldn't
    write `/etc/fstab` ("Operation not permitted"), so the mount options
    can't come from Disk Arbitration at boot.

The conditions, which S06-vm-lifecycle states in full ("Disks",
"Updates"):

1. **The host formats a new data disk** APFS before any guest has
   booted with it (or the guest formats it HFS+, open decision 1). The
   formatting process creates the image itself under a temporary name,
   checks that the device it attached is backed by that file, records
   the volume UUID, unmounts and detaches it, and only then gives it its
   final name. The host never attaches a file at the final name (open
   decision 2 for which process).
2. **`wb-guestd` mounts the data volume before it starts anything as a
   project user**, identified by the volume UUID the host recorded, at
   a fixed path outside `/Volumes`, with `nobrowse`, `nosuid`, `nodev`
   and ownership on. It refuses an encrypted volume. It reads the flags
   again with `statfs` before every session start, and starts no
   session without them, logging the mount it found and the rule.
3. **`wb-guestd` checks the volume root and every home with `lstat`**:
   real directories, root-owned with mode 755 for the root, and owned
   by the kept UID and GID with mode 700 for a home.
4. **`wb-hostd` keeps, per project user of a VM, the name, UID, GID,
   user and group `GeneratedUID`, and home path.** It allocates them
   itself, and never reuses one for any project on any data disk of
   this host. `wb-guestd` creates the user record with `GeneratedUID`
   first, and refuses a name, UID or GID that already exists.
5. **On a new system disk, the users are recreated before any session**,
   after the mount, and the homes are left as they are.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), no `sudo`.
Guest: macOS 27.0.1 from X17-image-build's sealed bundle `e5`, 4 vCPU,
4 GiB, a file-handle network with nothing behind it, and the data disks
as virtio block devices (automatic caching, full synchronization, as
S06-vm-lifecycle, "Disks") or one as USB mass storage. Three APFS clones
of `e5` (`g1`, `g2`, `g3`) were the system disks, one guest at a time.
Each data disk was a new 20 GiB sparse raw file.

| Boot | System disk | Data volumes | What |
|---|---|---|---|
| 1 | g1 | guest APFS | format, users, fill, checks, setuid |
| 2 | g2 | guest APFS | the swap: doesn't mount |
| 3 | g1 | guest APFS, passphrase APFS, USB APFS | fill on the passphrase volume, TCC on USB |
| 4 | g2 | guest APFS, passphrase APFS | the swap: neither unlocks |
| 5 | g2 | host APFS, guest HFS+ | users, fill, checks |
| 6 | g3 | host APFS, guest HFS+ | the swap, same `GeneratedUID` |
| 7 | g3 | host APFS, guest HFS+ | reboot: mount first; setuid without `nosuid` |
| 8 | g1 | host APFS, guest HFS+ | the swap, new `GeneratedUID` |

| Step | Time |
|---|---|
| Format a 20 GiB data disk: in the guest (APFS, HFS+), on the host (APFS) | 1.0 to 1.3 s |
| Disk Arbitration mounts the data volumes, after the guest kernel's boot time (boot 6) | 7.5 to 8.1 s |
| `wb-guestd`'s remount at the home path | 0.40 to 0.60 s (11 runs) |
| Create two users with new homes (`dscl`, `createhomedir`) | 2.1 to 2.4 s |
| Recreate two users on a new system disk, homes kept | 0.55 to 0.74 s |

The spike's checks wrote to the keychain files, so their checksums
changed between boots with the checks, not with the swap.

## What was not measured

- **A real Claude Code.** The image has no `claude`. The spike wrote the
  state layout of S06-vm-lifecycle ("Projects and sessions inside a VM")
  and read it back. I135 checks the layout with Claude Code.
- **Whether the system disk or the auxiliary storage holds the key** of
  a guest-made volume. Every swap here replaced both. The log names
  xART, which is on the system disk. S06-vm-lifecycle replaces both on
  an update, so the answer wouldn't change the design.
- **HFS+ against NFR02-fs-speed.** X05-fs-benchmark measured APFS.
- **Space given back to the host** on the host-made APFS volume, and
  whether a guest ever turns encryption on for it. I228 measures both.
- **The formatting steps of S06-vm-lifecycle.** The spike's script
  checked the device by protocol and size, used the deprecated
  `hdiutil attach`, and didn't rename the file or check its backing
  path.
- **A different macOS version** on the new system disk. All three were
  clones of one image.

## What it means for the specs

- **S06-vm-lifecycle**, "Disks": how the host formats a new data disk,
  and how `wb-guestd` mounts and checks it, with conditions 1 to 3.
  "Updates": the auxiliary storage is replaced with the system disk,
  the order of a swap, and the host state, allocation and creation
  rules of condition 4. "Projects and sessions inside a VM": the home
  path is fixed at creation, and who removes the home of a removed
  user (changed in this pull request).
- **S04-architecture**: with open decision 2 as recommended, its process
  and `wb-launcher` tables gain `wb-diskfmt`. Left to a follow-up after
  the decision, because I218 also edits those tables.
- **S05-cli** has no command that gives a VM a new data disk, which the
  refusals of "Disks" name (I229).
- **SEC00-index**: the host-mount control decided on I44 isn't written
  yet. Formatting a blank image that no guest has booted with is outside
  its scope, given condition 1.
- **S11-verification-and-spikes**, "Spikes": the list of answered spikes
  names X08-data-disk (changed in this pull request).

## Spike code

Branch `spike/x08-data-disk`, at
[0111c2b](https://github.com/wraithbox/wraithbox/tree/0111c2bab24fe3fc145d4ba1679f7b4663f53487/spikes/x08-data-disk):
the Swift `wb-vmd` stand-in with read-write and USB data disks, the
request client, the host-side format script, the guest scripts, the
host state of each cycle, and the raw results in `results/`.

**Status:** Answered 2026-10-12: yes, with conditions
