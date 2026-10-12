# X08-data-disk spike (throwaway)

Throwaway code for X08-data-disk (#22), never merged. Measured on
2026-10-12. The result is `docs/spikes/X08-data-disk-homes.md` on `main`.

## Parts

- `host-vmd/`: Swift `wb-vmd` stand-in, from X06-guest-xcode's
  `x06 serve`. `--data <raw image>` (repeatable) adds a read-write virtio
  data disk with automatic caching and full synchronization
  (S06-vm-lifecycle, "Disks"). `--usb <raw image>` adds the same disk as
  USB mass storage, which the guest sees as external. `--aux` overrides
  the auxiliary storage (built, not used).
- `req.py`: X06-guest-xcode's request client for the `x17-guestd` in
  X17-image-build's sealed bundle `e5` (`run` as root, `put`, `shutdown`).
- `r.sh`: one guest script as root through `req.py`, logged to
  `results/<stem>.jsonl`.
- `host-state-*.sh`: the per-user host state of each cycle (name, UID,
  GID, user and group `GeneratedUID`, home path), prepended to the guest
  scripts by `assemble.sh`.
- `host-format.sh`: the host makes a blank raw image with an APFS volume,
  as the user, before any guest has seen it.
- `euid.c`: prints the real and effective uid. A copy of `/usr/bin/id`
  is killed outside the system volume, so this is the setuid probe.
- `guest/`: the guest scripts. `mkusers.sh`, `fill2.sh`, `mount2.sh`,
  `check.sh` and `snap.sh` are the generic steps. `b1-*`, `b2-*`, `b4-*`,
  `c2-*` and `c3-*` are the per-boot probes.

## Build

```sh
swift build -c release --package-path spikes/x08-data-disk/host-vmd --scratch-path .scratch/x08-build
cp .scratch/x08-build/release/x08 .scratch/x08
codesign -f -s - --entitlements spikes/vm.entitlements .scratch/x08
cc -O2 -o .scratch/euid spikes/x08-data-disk/euid.c
```

## Boots

System disks: three APFS clones of X17's `e5` that the maintainer made
(`g1`, `g2`, `g3`), one guest at a time.

| Boot | System disk | Data disks | What |
|---|---|---|---|
| 1 | g1 | data.img (guest APFS) | cycle 1: format, users wbp1/wbp2, fill, checks, setuid |
| 2 | g2 | data.img | the swap: the volume doesn't unlock |
| 3 | g1 | data.img, data2.img (USB) | cycle 2: USB volume, passphrase volume WBPass, users wbq1/wbq2 |
| 4 | g2 | data.img | the swap: WBPass doesn't unlock either |
| 5 | g2 | data3.img (host APFS), data4.img (guest HFS+) | cycle 3: users wbr1/wbr2 and wbs1/wbs2 |
| 6 | g3 | data3.img, data4.img | swap, same GeneratedUID; remount policy |
| 7 | g3 | data3.img, data4.img | reboot: mount before any user process; setuid without nosuid |
| 8 | g1 | data3.img, data4.img | swap, new GeneratedUID |

## Findings in short

- A volume the guest formats APFS on its internal data disk is encrypted
  at rest with a key the new system disk can't unwrap (kernel:
  `AppleVPKeyStore ... xart error: -3`, `failed to unwrap volume key`).
  A passphrase volume fails the same way. A USB data disk isn't
  encrypted, but TCC (`kTCCServiceSystemPolicyRemovableVolumes`)
  refuses every process in the guest access to it.
- An APFS volume the host makes on the blank image, and an HFS+ volume
  the guest makes, aren't encrypted at rest and mount on any system
  disk. Recreated with the same UID, GID and GeneratedUID, everything
  compared equal. With a new GeneratedUID, the ACL entries naming the
  user stop applying, and a deny entry then allows.
- opendirectoryd, asked by root through `dscl`, refuses to change or
  delete a user's `GeneratedUID` and `NFSHomeDirectory` once the record
  has `UniqueID` and `PrimaryGroupID`. Setting `GeneratedUID` first
  works.
- Without `nosuid`, a setuid-root file on the data disk gives a project
  user root, also after the swap.
- Once a process has run as a user whose home is under the mount point,
  the kernel's System Policy denies another mount on that point
  (`deny(1) file-mount`) until the next boot. This also explains the
  failed remount on `/Volumes/wbhomes` in boot 1, which came after the
  checks had run as wbp1 and wbp2. The comments in `mount2.sh` and
  `b1-16-suid-on.sh` were written before that was found.
- `/etc/fstab` can't be written by the root daemon (`Operation not
  permitted`).
