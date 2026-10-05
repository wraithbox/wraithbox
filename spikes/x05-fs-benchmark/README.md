# X05-fs-benchmark spike (throwaway)

Throwaway code for X05-fs-benchmark, never merged. The result is
`docs/spikes/X05-fs-benchmark.md` on `main`.

Host: Apple M2 (4 performance and 4 efficiency cores), 16 GB, macOS
27.0.1 (26A434), FileVault on, one Endpoint Security extension active
(`results/host-env.txt`). Other agents' work ran on the host during the
runs (load average 3 to 4). Guest: macOS 27.0.1, an APFS clone of the
X18-vsock-handoff `pristine` bundle (installed, never booted),
provisioned by this spike, 4 vCPU (8 in the `cpu8` runs), 8 GiB.

The VM under test has one NIC, `VZFileHandleNetworkDeviceAttachment` on a
`SOCK_DGRAM` socket pair, and x05-netd allows no names, so the guest
reaches nothing but the host-only SSH forward. No NAT attachment and no
shared directory, in any boot. The host never mounts, formats or reads a
data image: the guest formats it, and the workloads go in as tar streams
over SSH.

## Parts

- `host-vmd/`: Swift, the `wb-vmd` stand-in, copied from X03-network-path's
  `x03`. `--data <image> --cache automatic|cached|uncached --sync
  full|fsync|none` attaches a raw or ASIF image as the data disk with
  `VZDiskImageStorageDeviceAttachment`. `X05_CPUS` overrides the CPU count.
- `go/cmd/x05-netd/`: X03-network-path's `x03-netd`, unchanged, for the
  SSH forward (127.0.0.1:2222 to the guest's port 22).
- `bench.mjs`: the workloads, run by the same node binary on the host and
  in the guest: `npm ci --offline` (the docs site's dependencies, 536
  packages, 17k files, 317 MB), `git status` in a shallow clone of
  nodejs/node v24.9.0 (43k files, 814 MB), a Go incremental build
  (x05-netd with vendored gVisor, one changed file) and a clean Go
  build, plus two probes of the sync modes: 500 appends of 4 KiB each
  followed by `fsync` (libuv issues `F_FULLFSYNC`), and 512 MiB of
  sequential writes. Warm runs, and cold runs after `sudo purge` in the
  guest. The host can't run `purge` without a password, so it has warm
  runs only.
- `prep-tools.sh`, `prep-npm.sh`, `prep-repo.sh`: build the work
  directory on the host (toolchains, npm project and cache, repository).
- `push.sh`: tar a host directory into the guest over SSH.
- `guest/sudoers.sh`, `guest/datadisk.sh`: passwordless sudo for the spike
  user; format the blank data disk in the guest, Spotlight off.
- `guest/ramdisk.sh`: the CPU control, the same workloads on a guest RAM
  disk.
- `boot.sh`, `gbench.sh`, `gstop.sh`, `matrix.sh`: boot with a disk
  setting, bench in the guest, shut down, bench on the host while no VM
  runs, for each setting.
- `crash.mjs`, `crash.sh`, `crash-all.sh`, `guest/fsck.sh`: the
  kill-mid-write test. The guest appends 4 KiB records with
  `F_FULLFSYNC` after each and prints `ack <n>`, with an unsynced 64 KiB
  file after each record. After N seconds the host sends SIGKILL to
  either this spike's `x05` process or the Virtualization XPC service
  that holds this VM's data image open (found with `lsof` on the image
  path). The next boot runs `fsck_apfs -n` and checks every record.
- `trim.sh`: does the image shrink on the host when the guest deletes
  data?
- `stats.py`: medians, and each setting's share of host throughput.

## Build and run

```sh
S=../../.scratch
B=~/Library/Caches/wraithbox-spikes/x05
(cd host-vmd && swift build -c release --scratch-path ../$S/x05-build)
cp $S/x05-build/release/x05 $S/x05
codesign -f -s - --entitlements host-vmd/x05.entitlements $S/x05
(cd go && GOWORK=off go build -o ../$S/x05-netd ./cmd/x05-netd)
umask 077; openssl rand -hex 12 > $S/guest-pass
cp -c -R ~/Library/Caches/wraithbox-spikes/x18/pristine $B/base
./run.sh $S provision $B/base 25 --provision x05 $S/guest-pass
# in the provisioning boot: guest/sudoers.sh, push.sh of tools/, bench.mjs, guest/datadisk.sh
sh prep-tools.sh $B/work.noindex
sh prep-npm.sh $B/work.noindex ../../packages/wraithbox-doc/package.json
sh prep-repo.sh $B/work.noindex .
cp -c -R $B/base $B/run
diskutil image create blank --format ASIF --size 16g --fs None $B/asif.asif
truncate -s 16g $B/raw.img
./boot.sh $S setup $B/run $B/asif.asif automatic full
./gssh.sh $S sh /Users/x05/datadisk.sh 16000000000
sh push.sh $S $B/work.noindex npm /Volumes/data/work.noindex   # and repo, gobuild
./gstop.sh $S
./matrix.sh $S $B/run $B/asif.asif asif 5 automatic:full automatic:fsync ...
X05_CPUS=8 ./matrix.sh $S $B/run $B/raw.img raw-cpu8 5 automatic:full automatic:none
sh crash-all.sh $S $B/run $B/raw.img
python3 stats.py > results/stats.txt
```

## Results

- `stats.txt`: the summary the result page quotes.
- `<image>-<cache>-<sync>-bench.jsonl`: every guest run, one JSON line
  each. `asif-probe-bench.jsonl` is a first ASIF automatic/full boot
  before the matrix, `ramdisk-cpu4-bench.jsonl` the RAM disk control.
- `host-bench.jsonl`: the host runs, one set after each guest setting.
- `kill-*-kill.txt`, `kill-*-check.txt`, `kill-*-acks.txt`: the kill
  test (the acks file trimmed to its head and tail, with the counts).
- `trim-raw.txt`, `trim-asif.txt`: image size on the host after the guest
  writes and deletes 1 GiB.
- `*-vmd.jsonl`, `*-netd.jsonl`: each boot's x05 and x05-netd logs.
