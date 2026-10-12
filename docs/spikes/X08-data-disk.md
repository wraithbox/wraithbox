# X08 - Data disk for homes

**Purpose:** Whether guest user homes can live on a second disk and
survive a replacement of the system disk, and what the host must keep
to recreate the users.

Brief: B22-data-disk

## For review

- **Decides:** the answer to X08-data-disk (I22), and how the data disk
  gets its filesystem, which S06-vm-lifecycle, "Disks", now states, with
  the steps of "Updates" that recreate the project users.
- **You are approving:** the answer "yes, with conditions", and the
  change to how a data disk is made. A volume the guest formats APFS on
  its data disk is lost with the system disk: the guest encrypts it at
  rest with a key it keeps on the system disk, and a fresh system disk
  can't unwrap it. Instead, the host formats each new data disk APFS
  before any guest has seen it, which gives a volume without that key.
  The host never attaches the disk again. The users are recreated from
  host state with the same UID, GID and `GeneratedUID`.
- **Controls touched:** SEC08-proj-isolation is kept on two conditions:
  the data volume is mounted `nosuid` and `nodev` with ownership on,
  because a setuid-root file planted there gives a project user root
  after a swap, and the host never gives a UID or `GeneratedUID` to a
  second project. SEC11-root-gains-nothing: a guest root that wrote to
  the data disk keeps what it wrote after a swap, because the swap
  replaces only the system disk. The host's new step, formatting a blank
  image, falls under the I44 decision that the host never mounts a disk
  after a guest has booted from it.
- **Assumed:** that a guest never turns encryption on for a volume it
  didn't format, which held for four boots here. That the space a guest
  frees on the host-made volume goes back to the host as on a
  guest-made one (X05-fs-benchmark measured the guest-made one).
- **Open decisions:**
  1. Who formats the data disk. Recommended: the host, APFS, before the
     first boot, as above. The alternative is the guest formatting it
     HFS+ (Mac OS Extended), which the spike showed survives the swap
     as well, and which keeps the host out of formatting. HFS+ has no
     clones and no snapshots, and X05-fs-benchmark didn't measure it.
     I44 writes the SEC control for host mounts, and with option A its
     text names this exception.
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
   rest)". On a fresh clone of the image, Disk Arbitration failed to
   mount it at boot, and by hand the kernel logged `AppleVPKeyStore:
   volume ... unlock failed (-25)` and `failed to unwrap volume key`.
   The system disk it was made on still mounted it at the next boot.
2. **A passphrase volume doesn't survive it either: no.** An APFS
   volume added with `diskutil apfs addVolume -passphrase` failed to
   unlock with its passphrase on the fresh system disk
   (`AppleVPKeyStore: xart error: -3`, `failed to decode vek`). So the
   key needs more than the passphrase. The log points at xART, the
   anti-replay store, which is on the system disk. The spike didn't
   find out whether the auxiliary storage plays a part.
3. **A USB data disk can't be used: no.** Attached as USB mass storage,
   the disk is external to the guest, and its APFS volume isn't
   encrypted. TCC refuses every process in the guest access to it
   (`kTCCServiceSystemPolicyRemovableVolumes`), root included, and a
   job launchd starts directly. The spike didn't try a grant, which on
   macOS comes from MDM or from a person at the guest's screen.
4. **A host-formatted APFS volume survives: yes.** The host creates a
   sparse raw file, attaches it as the user without mounting
   (`hdiutil attach -nomount`, as in X17-image-build), runs
   `diskutil eraseDisk APFS`, and detaches it. On the host, the image
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
   same UID and GID but new `GeneratedUID`s, owners and modes still
   matched, and every check passed except one: the ACL entries naming
   the user or its group now show the old UUID unresolved, and the file
   whose ACL denied the user write took the write. A deny entry fails
   open, and an allow entry stops granting.
10. **`nosuid` is needed: verified.** A setuid-root probe that guest
    root put in a home gave the project user effective uid 0 when the
    volume was mounted without `nosuid`, both before and after the swap
    (the HFS+ volume, boot 7). With `nosuid` it ran with the user's own
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

The conditions:

1. **The host formats a new data disk** APFS before the first boot that
   attaches it (or the guest formats it HFS+, open decision 1). The host
   checks that the device it formats is the image it just attached, and
   never attaches the image again.
2. **`wb-guestd` mounts the data volume before it starts anything as a
   project user**, at a fixed path outside `/Volumes`, with `nobrowse`,
   `nosuid`, `nodev` and ownership on, and checks the mount's flags.
   Without them, it doesn't start a session, and logs the mount it found and
   the rule.
3. **`wb-hostd` keeps, per project user of a VM, the name, UID, GID,
   user and group `GeneratedUID`, and home path**, and gives none of the
   numbers to another project on that data disk. `wb-guestd` creates
   the user record with `GeneratedUID` first.
4. **On a new system disk, the users are recreated before any session**,
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
- **Whether the auxiliary storage holds part of the key** of a
  guest-made volume. The log names xART on the system disk, and the
  product replaces that disk, so the answer wouldn't change.
- **HFS+ against NFR02-fs-speed.** X05-fs-benchmark measured APFS.
- **Space given back to the host** on the host-made APFS volume.
- **A different macOS version** on the new system disk. All three were
  clones of one image.

## What it means for the specs

- **S06-vm-lifecycle**, "Disks": the host formats a new data disk before
  the first boot, and `wb-guestd` mounts it with the options of condition
  2. "Updates": the order of a system disk swap and the host state of
  condition 3. "Projects and sessions inside a VM": the home path is
  fixed at creation (changed in this pull request).
- **SEC00-index**: the host-mount control decided on I44 isn't written
  yet. With open decision 1A, its exception for image building also
  covers formatting a new data disk. Left to I44.
- **S11-verification-and-spikes**, "Spikes": the list of answered spikes
  names X08-data-disk (changed in this pull request).

## Spike code

Branch `spike/x08-data-disk`, at
[c37c09a](https://github.com/wraithbox/wraithbox/tree/c37c09a2c21fa9327ff859b56d915b397d075d80/spikes/x08-data-disk):
the Swift `wb-vmd` stand-in with read-write and USB data disks, the
request client, the host-side format script, the guest scripts, the
host state of each cycle, and the raw results in `results/`.

**Status:** Answered 2026-10-12: yes, with conditions
