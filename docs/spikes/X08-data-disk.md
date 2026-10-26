# X08 - Data disk for homes

**Purpose:** Whether guest user homes can live on a second disk and
survive a replacement of the system disk, and what the host must keep
to recreate the users.

Brief: B22-data-disk. Follow-up (I228): B228-data-disk-followup

## For review

- **Decides:** the answer to X08-data-disk (I22), how the data disk
  gets its filesystem, and which process formats it, which
  S06-05-disks, now states, with the steps of "Updates"
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
  S06-05-disks, that make sure the image is that blank
  file. SEC12-least-privilege: decision 2, below.
- **Assumed:** that a guest never turns encryption on for a volume it
  didn't format, which held for four boots here, and that the space a
  guest frees on the host-made volume goes back to the host as on a
  guest-made one (X05-fs-benchmark measured the guest-made one). I228
  measured both: encryption stayed off, and space goes back at each
  clean shutdown or cold boot ("Follow-up (I228)", below). That the
  key of a guest-made volume needs the old system disk is inferred
  from the kernel log: the clones also replaced the auxiliary
  storage, and the spike didn't separate the two.
- **Decisions:** decided 2026-10-12, as recommended, in the I22
  comment that starts `Decision (2026-10-12):`. The assumptions above
  stay with I228.
  1. Who formats the data disk. Decided: the host, APFS, before the
     first boot, as above. The alternative is the guest formatting it
     HFS+ (Mac OS Extended), which the spike showed is kept across the
     swap as well, and which keeps the host out of formatting. HFS+ has
     no clones and no snapshots, and X05-fs-benchmark didn't measure it.
  2. Which host process runs `diskutil`. `wb-hostd` can't: under its
     self-sandbox it starts only fixed `wb-launcher` entries, with no
     arguments (S04-architecture). Decided: a fixed `wb-launcher`
     entry per VM slot for a small shim, `wb-diskfmt`
     (`wb-diskfmt-work`, `wb-diskfmt-isolated`), each with its VM's
     bundle as the profile parameter, never a path from the request,
     which runs the steps of "Disks". S04-architecture and
     S06-vm-lifecycle are written for decision 2A.
     I218 (one `wb-vmd` per VM, its launcher entries) also edits
     S04-architecture and rebases onto this. I228 measured a confined
     `diskutil` run, which works ("Follow-up (I228)", below).
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
S06-05-disks) or one as USB mass storage. Three APFS clones
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
  state layout of S06-08-projects-and-sessions
  and read it back. I135 checks the layout with Claude Code.
- **Whether the system disk or the auxiliary storage holds the key** of
  a guest-made volume. Every swap here replaced both. The log names
  xART, which is on the system disk. S06-vm-lifecycle replaces both on
  an update, so the answer wouldn't change the design.
- **HFS+ against NFR02-fs-speed.** X05-fs-benchmark measured APFS.
- **Space given back to the host** on the host-made APFS volume, and
  whether a guest ever turns encryption on for it. I228 measured both
  ("Follow-up (I228)", below).
- **The formatting steps of S06-vm-lifecycle.** The spike's script
  checked the device by protocol and size, used the deprecated
  `hdiutil attach`, and didn't rename the file or check its backing
  path, and didn't run `diskutil` under a profile. I228 ran the steps
  confined ("Follow-up (I228)", below).
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
- **S04-architecture**: written for decision 2A. The
  process table gains `wb-diskfmt`, and "Each host daemon is
  self-sandboxed" its two `wb-launcher` entries and its profile
  (changed in this pull request).
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

## Follow-up (I228): the host-made volume

Brief: B228-data-disk-followup

### For review

- **Decides:** the answers to the three questions that X08-data-disk
  left to I228, and whether `wb-guestd` keeps mounting the data volume
  with `nobrowse` (decision 3, open).
- **You are approving:** "yes" for encryption and for the confined
  `diskutil`, and "yes, with conditions" for the space given back. While
  `wb-guestd` mounts the volume with `nobrowse`, space the guest frees
  goes back to the host only when the volume is unmounted or mounted
  again: at a clean guest shutdown or at the next cold boot, not at a
  restore from saved state. S06-05-disks now says so.
- **Controls touched:** none weakened. SEC15-no-guest-disk-mount keeps
  its one exception for a blank image, now measured confined.
  SEC12-least-privilege: the profile of S04-04-vm-processes suffices,
  with no `hdiutil`. SEC13-bounded-resources counts the file's
  `st_blocks`, which still count deleted data until the next clean
  shutdown or cold boot.
- **Assumed:** that a later point release keeps the volume
  unencrypted. All three guests ran macOS 27.0.1, and 27.2 wasn't
  tried (I425).
- **Decision 3 (open):** keep `nobrowse` (the spec as it is, space
  returns per clean shutdown or cold boot) or drop it from the mount and the check
  (space returns within about 20 s). Recommended: keep it. The brief
  holds the options, and the spec keeps `nobrowse` until the maintainer
  decides.

### Question

From I228, which X08-data-disk's "What was not measured" names:

1. Does a guest ever turn encryption on for a volume that `wb-diskfmt`
   made on the host?
2. Does that volume give the space the guest frees back to the host,
   as X05-fs-benchmark measured for a guest-made one?
3. Can `diskutil` attach and format the image under `wb-diskfmt`'s
   profile? Does the file keep `st_blocks` 0 through `diskutil image
   attach --noMount`, and does the profile need `hdiutil`?

### Answer

1. **Encryption stays off: yes.** In three boots, each a fresh system
   disk, the guest reported the volume "Encrypted: No" and "FileVault:
   No", with no "Encrypted at rest" in `diskutil apfs list`, before and
   after writing to it. The same disk went from boot b1 to b2 to b3, so
   b2 and b3 are system disk swaps. `wb-guestd`'s own mount code
   accepted it each time.
2. **Space goes back: yes, with conditions.** With the mount options
   of S06-05-disks, `nobrowse,nosuid,nodev,owners`, deleted data
   doesn't go back to the host while the volume is mounted. It
   was still allocated for 10.5 min in the longest wait. It goes back in
   full when the volume is unmounted or mounted again: a clean guest
   shutdown, or the mounts of the next cold boot. Without
   `nobrowse`, it went back within 20 s of the delete. Mounting with
   each option alone showed that `nobrowse` alone holds it back.
3. **A confined format works: yes.** `TestHostFormat` ran
   `wb-diskfmt` under its own profile, which allows `diskutil` and no
   other program. It attached, checked the backing path twice through
   the I/O Registry, formatted, ejected, and renamed the image. The
   file had `st_blocks` 0 at the check right before `eraseDisk`, after
   the attach. `hdiutil` isn't needed.

Two of the `datamount` conformance checks of C09-guest-and-sessions
fail on a real guest because of how the tests are written, not because
of the mount: `TestSetuidIgnored` and `TestRemountRefused`. See
"Conformance" below.

### Measurements

Host: Apple M2, macOS 27.0.1 (26A434). Guests: macOS 27.0.1 (26A434),
each a fresh `mise run vm:clone dev/base i228/bN`. The data disk was
the 8 GiB image that `TestHostFormat` made, moved into each clone
before its first boot.

**The confined format** (`results/host-format.txt`), at main `3dd4c0c`:

| Step | Result |
|---|---|
| `self-sandbox applied`, profile `wb-diskfmt` | confined |
| `diskutil image attach --noMount` | 0.20 s, no backing path in its output |
| Backing path check, at the attach and at the erase | accepted both times |
| Identity check with `st_blocks` 0, at create and at the erase | passed |
| `diskutil eraseDisk APFS` | 1.09 s |
| `diskutil eject` | 0.28 s |
| Read outside the bundle | denied |
| The image after the rename | 8 GiB, 5,603,328 bytes allocated |
| Whole test | 1.77 s, PASS |

**Encryption and the mount** (`results/b1-*.txt`, `b2-boot-check.txt`,
`b3-boot-check.txt`). `x8mount` runs `wb-guestd`'s `DataMount` (mount,
then check) without `wb-hostd`.

| Boot | `diskutil info` | `apfs list` | Mount | Check | `TestMounted` |
|---|---|---|---|---|---|
| b1, first mount | Encrypted: No, FileVault: No | FileVault: No | 1.17 s, accepted | 0.12 s, accepted | PASS |
| b1, 25 min later, after every space run | Encrypted: No, FileVault: No | FileVault: No | | | |
| b2, swap | Encrypted: No, FileVault: No | FileVault: No | 0.79 s, accepted | 0.17 s, accepted | PASS |
| b3, swap | Encrypted: No, FileVault: No | FileVault: No | 0.79 s, accepted | 0.11 s, accepted | not run |

In b3 the `scp` that copies `datamount.test` to the guest failed, and
the run after it found no file. The `PASS` lines that follow that
error in `b3-boot-check.txt` can't come from a run on b3, so
`TestMounted` counts as not run there. The mount and the check above
ran.

The guest saw the volume as internal and solid state, with owners
enabled. `statfs` reported `nosuid`, `nodev` and `nobrowse` set, with
ownership on. On the first mount the root was `0:0` mode 0775, and
`wb-guestd` claimed it to mode 755.

**Space given back** (`results/*space*.txt`, `stop-and-start.txt`).
`space.sh` writes random data in the guest, syncs, deletes it, and
reads the image's `st_blocks` on the host every 10 s.

| Mount | Written | Allocated after the delete |
|---|---|---|
| `/private/var/wbhomes`, `nobrowse,nosuid,nodev,owners` (b1) | 2 GiB | 2,172,194,816 B for 180 s |
| the same (b1) | 512 MiB | 543,748,096 B for 30 s, then 10.5 min more |
| the same (b2) | 1 GiB | 1,080,729,600 B for 61 s |
| `/private/var/x8e`, the same options (b1) | 512 MiB | 543,801,344 B for 30 s |
| `/private/var/x8b`, `nobrowse` alone (b1, b3) | 512 MiB | 543,784,960 and 543,903,744 B for 30 s |
| `/Volumes/WraithBoxData`, Disk Arbitration's mount (b1) | 1 GiB | 6,930,432 B at once |
| `/private/var/x8a`, no options (b1, b3) | 512 MiB | 6,918,144 and 7,147,520 B at 20 s |
| `/private/var/x8c`, `nosuid,nodev` (b1) | 512 MiB | 6,918,144 B at 20 s |
| `/private/var/x8d`, `owners` (b1) | 512 MiB | 6,926,336 B at 20 s |

| Event | Allocated before | After |
|---|---|---|
| Unmount from `/private/var/wbhomes`, mount at `/Volumes` (b1) | 2,172,194,816 B | 6,942,720 B |
| Clean shutdown in the guest, `shutdown -h` (b1) | 543,748,096 B | 6,860,800 B |
| `mise run vm:stop`, guest not down in the grace time, stopped (b2) | 1,080,729,600 B | 1,080,729,600 B |
| Next boot with that disk, `wb-guestd`'s mount (b3) | 1,080,729,600 B | 7,045,120 B |

**Conformance** (`results/b1-conformance.txt`,
`b1-setuid-probe.txt`, `b1-remount-probe.txt`). Run as root on b1,
`datamount.test` passed `TestMounted` and failed the other two:

- `TestSetuidIgnored`: its control probe, a setuid-root copy in
  `/private/var/tmp`, ran as uid 501, so the test stopped before the
  data volume. The same copy started with `sudo -u nobody` or
  `sudo -u wbdev` ran with effective uid 0, and so did a C probe. That
  points at how the test starts the probe, not at macOS. On the
  data volume with `nosuid`, the C and Go probes ran as uid -2 (nobody).
  Remounted without `nosuid`, they ran as uid 0. So `nosuid` works on
  this volume and is needed, as X08-data-disk found.
- `TestRemountRefused`: `mount -u -o <option>` on macOS 27 replaces
  the flags rather than adding one. `-o suid` dropped `nodev` and
  `nobrowse`, and `-o nosuid` left only `nosuid`. The test's cleanup
  remounts with `nosuid` alone, so the check refused the mount after
  it. Remounting with `nosuid,nodev,nobrowse` restores it.

### What was not measured

- **A product guest with `wb-hostd`.** `x8mount` ran `wb-guestd`'s mount
  code by hand, as root over SSH, in the dev image.
- **macOS 27.2 or later as the guest.** A 27.2 restore image is
  25.6 GB, and the host had 50 GiB free (I425).
- **Space given back with `nobrowse` over hours.** The longest wait
  was 10.5 min. Each delete was one file.
- **A guest that crashes or is stopped by force and then never starts
  again.** Its space is still allocated until the next start (b2 to b3).
- **A restore from saved state** (S06-06-devices-and-warm-start,
  "Warm start"). The guest doesn't boot and `wb-guestd` doesn't mount again
  (S06-05-disks, "The mount"), so the space deleted before the save
  presumably stays allocated across the restore, until a clean
  shutdown or a cold boot.
- **Who owns the volume's `.fseventsd`.** In b1 it was owned by
  `wbdev` (uid 501), the dev image's user. S06-05-disks refuses a UID
  that owns a top-level entry when it creates a user. A product guest
  has its own users, and I424 checks it there.

### What it means for the specs

- **S06-05-disks**, "Disks": the space the guest frees goes back to the
  host when `wb-guestd`'s mount is unmounted or mounted again, not when
  the guest deletes (changed in this pull request).
- **S06-13-data-disk-format**: the three I228 claims are measured
  (changed in this pull request).
- **S04-04-vm-processes**: the profile stands as written, with no
  `hdiutil`.
- **C09-guest-and-sessions**: `TestSetuidIgnored` and
  `TestRemountRefused` need fixing in the tests (I424).
- **Decision 3** would change S06-05-disks, "The mount" and "Before
  every session start", and `internal/platform/guestmount` if
  `nobrowse` goes.

### Spike code

Branch `spike/x8-data-disk-followup`, at
[81c1f21](https://github.com/wraithbox/wraithbox/tree/81c1f214461856075fb4bb2a26fcabf779f60dbf/spikes/x8-data-disk-followup):
`x8mount` (`wb-guestd`'s mount without `wb-hostd`), the guest scripts,
the space scripts, the probes of the two failing conformance checks,
and the raw results in `results/`.

**Status:** Answered 2026-10-12: yes, with conditions. Decided 2026-10-12 (I22): 1A, the host formats the data disk APFS, and 2A, in the `wb-diskfmt` shim. Follow-up answered 2026-10-26 (I228): the host-made volume is not encrypted, gives freed space back at each clean shutdown or cold boot while mounted `nobrowse`, and formats under the confined shim. Decision 3 (`nobrowse`) awaits the maintainer
