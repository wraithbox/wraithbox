# X08-data-disk spike (throwaway, unfinished)

Throwaway code for X08-data-disk (#22), never merged. Not run yet: the
host tool needs an ad-hoc signature with the
`com.apple.security.virtualization` entitlement before it can start a VM,
and the agent that wrote this was not allowed to run `codesign` on the
host. Nothing below has been measured.

## Parts

- `host-vmd/`: Swift `wb-vmd` stand-in, copied from X06-guest-xcode's
  `x06 serve`, plus `--data <raw image>`: a read-write data disk attached
  with automatic caching and full synchronization (S06-vm-lifecycle,
  "Disks").
- `req.py`: X06-guest-xcode's request client. The guest daemon is the
  `x17-guestd` already in X17-image-build's sealed bundle `e5`
  (`hello`, `run` as root, `put`, `shutdown`), so no daemon swap.

## Build

```sh
S=.scratch
swift build -c release --package-path spikes/x08-data-disk/host-vmd --scratch-path $S/x08-build
cp $S/x08-build/release/x08 $S/x08
codesign -f -s - --entitlements spikes/x08-data-disk/host-vmd/x08.entitlements $S/x08   # needs the maintainer's OK
```

## Plan

Bundle: an APFS clone of `~/Library/Caches/wraithbox-spikes/x17/e5`.
Data disk: a new sparse raw file the host creates and never reads.

1. Boot 1 (`x08 serve g1 sock --data data.img`): find the blank data
   disk, format it APFS in the guest, record `diskutil info` (owners on
   or off, internal or external, automount at boot), mount it with
   `nobrowse`, then with and without `nosuid`.
2. Create two project users with fixed UID, GID, `GeneratedUID` and a
   home on the data volume (`dscl` and `sysadminctl -UID`), home mode
   700. Fill each home as the user: Claude Code state (`~/.claude`,
   `~/.claude.json`), files with several modes, xattrs, an ACL, a
   symbolic link, and a keychain made with `security create-keychain`.
   Record checksums and `ls -lane`. Check the second user can't read the
   first one's home. As root, plant a setuid-root copy of `/usr/bin/id`
   in the second user's home.
3. Replace the system disk: fresh clones of `e5/sys.img` and
   `e5/aux.img`, same data disk.
4. Boot 2: what the data volume looks like before users exist (mounted,
   owners, numeric IDs). Recreate the users from the recorded host state,
   once with the same `GeneratedUID` and once with a new one, and compare
   ownership, modes, ACLs, xattrs, checksums, keychain unlock, Claude Code
   state, the cross-user read check, and the planted setuid binary with
   and without `nosuid`.
5. Time the format, the mount at boot, and the user recreation.
