# X25-vmd-sandbox spike (throwaway)

Throwaway code for X25-vmd-sandbox (#64), never merged. The result is
`docs/spikes/X25-vmd-sandbox.md` on `main`.

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0.
Guest: macOS 27.0.1, the X18-vsock-handoff configuration (4 vCPU,
4 GiB, system and data disk, `VZVirtioSocketDevice`,
`VZFileHandleNetworkDeviceAttachment`, entropy, one display), with the
X18 guest daemon (`x18-guestd`, gRPC on vsock port 1024) installed by
`guest/setup.sh`, copied from `spike/x18-vsock-handoff`.

## What it is

`Sources/x25/main.swift` is a `wb-vmd` stand-in. With `--sb <profile>`
it applies a Seatbelt profile to itself with
`sandbox_init_with_parameters` (declared with `@_silgen_name`, no
bridging header) before it touches the Virtualization framework, then
runs probes that the profile should deny, then the command: `install`,
`provision`, `boot` (cold boot to the guest's vsock listener, optional
save), `restore`, with options that add devices pointing outside the
bundle. Before confining it resolves the per-user cache directory with
`confstr(_CS_DARWIN_USER_CACHE_DIR)` (`X25_PRECACHE=0` skips that) and
makes the socketpair that stands in for the hand-off channel.

## Profiles

- `profiles/report-all.sb`: allow everything and log it; the discovery
  runs.
- `profiles/vmd-v1.sb` to `vmd-v5.sb`: the iterations, each from the
  denials of the one before.
- `profiles/vmd.sb`, `vmd-min.sb`, `vmd-split.sb`: v5 cut into sections
  for `ablate.sh`.
- `profiles/vmd-final.sb`: the result. `vmd-final-di.sb`: the same plus
  the disk image service, a hypothesis that was wrong.
- `profiles/vmd-install.sb`: `vmd-final.sb` plus what installing from a
  restore image needs.

## Scripts

- `build.sh <scratch>`: build and ad-hoc sign with
  `com.apple.security.virtualization`.
- `run.sh <name> <x25 args>`: run once, keep stdout, stderr and the
  Sandbox log lines (`results/<name>.jsonl`, `.err`, `.sandbox.txt`).
- `sblog.sh`: the Sandbox log lines between two times, counted per
  distinct operation.
- `ablate.sh`: boot once per profile section, without that section.
- `addon.sh`: restore a state saved unconfined, under a profile plus one
  extra rule.
- `devices.sh`: boot once per extra device (NAT, shared directory, disk,
  serial port file, SPICE clipboard, microphone), each pointing outside
  the bundle.
- `series.sh`: N restores confined and N unconfined, alternating, each
  from a fresh APFS clone.
- `outside.sh`: the files outside the bundle that the device runs use.
- `guest/check.sh`: over SSH on a NAT NIC, check from inside the guest
  whether NAT reaches the internet, the share mounts, and the extra
  disk's marker is visible.

## Build and run

```sh
S=../../.scratch                  # from spikes/x25-vmd-sandbox
./build.sh $S
X=~/Library/Caches/wraithbox-spikes/x25
C=$(getconf DARWIN_USER_CACHE_DIR | sed 's#^/var#/private/var#; s#/$##')
cp -c -R ~/Library/Caches/wraithbox-spikes/x18/pristine $X/base   # installed, not provisioned
guest/setup.sh $S/x25 $X/base $S/x18-guestd $S                   # x18-guestd built from spike/x18-vsock-handoff
cp -c -R $X/base $X/f1
./run.sh final-boot-save --sb profiles/vmd-final.sb -D BUNDLE=$X/f1 -D CACHE=$C boot $X/f1 --save
./run.sh final-restore-2 --sb profiles/vmd-final.sb -D BUNDLE=$X/f1 -D CACHE=$C restore $X/f1
./ablate.sh $X/base $X $C
./devices.sh dev-final profiles/vmd-final.sb $X/f2 $C $X-outside 26:fe:9f:70:a5:07
./series.sh $X/su $X $C profiles/vmd-final.sb 10
```

## Results

- `discover-boot.sandbox.txt`, `discover-restore.*`: every operation
  the process did under `report-all.sb`.
- `v1-boot` to `v5-restore`: the iterations.
- `ablate-v5.txt`, `ablate-extensions.txt`, `ablate-split.txt`: which
  sections a boot needs. Each line is the result of the boot without
  that section.
- `final-*`: boot, save, restore, restore again, the bundle parameter
  set to the parent of all bundles (`final-vms-root`), the hand-off of
  a vsock descriptor (`final-handoff`), and the run without the cache
  directory resolved before confining (`final-no-precache`).
- `matrix-*`, `matrix2-*`, `addon-round1.txt`, `addon-round2.txt`,
  `series-cpu-name-denied.txt`: a state saved by a process that can't
  read `machdep.cpu.brand_string` doesn't restore in one that can, and
  the other way round. `matrix2-*` is after the profile allows it. The
  per-run files `addon-1` and `addon-2` are from round 2 (round 1's were
  overwritten). Round 1 is only in `addon-round1.txt`.
- `series.txt`: 10 restores confined and 10 unconfined.
- `devices-final.txt`, `devices-unconfined.txt`, `dev-*`: the device
  runs. `guest-*-check.txt`: what the guest saw.
- `install-confined.*`: a macOS install under `vmd-install.sb`.
