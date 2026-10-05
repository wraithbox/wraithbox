# X05 - Filesystem benchmark

Brief: B19-fs-benchmark

**Purpose:** The result of the spike for I19. How does the data disk
perform with default versus relaxed write-through settings, against the
host, on the NFR02-fs-speed workloads?

## For review

- **Decides:** the data disk settings. The data disk is a raw sparse
  image, attached with automatic host caching and full synchronization,
  the setting that keeps every flushed write through a host crash. The
  relaxed settings gave the NFR02-fs-speed workloads nothing that the
  run-to-run spread doesn't cover, so S06-vm-lifecycle "Disks" no longer
  allows them.
- **You are approving:** the answer "no", the measurements, and the
  S06-vm-lifecycle "Disks" text. `git status` on a large repository runs
  faster in the guest than on the host, and the Go incremental build is
  at the 80% line. `npm ci` reaches 71% of host throughput with the
  chosen setting, and 75% with the most relaxed one. On a RAM disk in
  the guest, where no virtual disk is involved, it reaches 77%. The
  shortfall is in the guest, not in the disk settings.
- **Controls touched:** none. The host never mounted, formatted or read a
  data image. The guest formatted each one, and the workloads went in as
  tar streams over SSH. The VM had only the file-handle network device,
  with no name allowed, and no shared directory.
- **Assumed:** that a full-sync image keeps flushed writes through a
  host power loss, as Apple's header says. The spike killed the VM's
  processes, not the host. That the host baseline is fair: the host has
  FileVault and an Endpoint Security extension, and other agents' work
  ran on it during the runs (see "Measurements").
- **Open decisions:**
  1. What to do about `npm ci` at 71% of host throughput, below the 80%
     of NFR02-fs-speed with every disk setting. Recommended: keep
     NFR02-fs-speed as written, and find where the guest loses the time
     in a follow-up (I97) before the V1-M6-release-gate benchmark.
- **Brief:** B19-fs-benchmark

## Question

From X00-index: data disk with default versus relaxed write-through
settings, against the host, on the NFR02-fs-speed workloads. The issue
asks for each combination of caching mode and synchronization mode of
`VZDiskImageStorageDeviceAttachment`, on an ASIF and a raw image, each
workload five times warm and cold in the guest and on the host, and a
kill of the VM process mid-write in the relaxed modes.

## Answer

**No.** NFR02-fs-speed asks for at least 80% of host throughput on
`npm ci`, `git status` on a large repository, and an incremental build.
With 4 vCPU, the S06-vm-lifecycle default, and the setting chosen here
(raw image, automatic caching, full sync), the warm guest reached:

| Workload | Host p50 | Guest p50 | Share of host |
|---|---|---|---|
| `npm ci` | 2.33 s | 3.26 s | 71% |
| `git status`, 43k files | 0.250 s | 0.182 s | 137% |
| Go incremental build | 0.439 s | 0.542 s | 81% |

No disk setting changes that answer:

1. **The sync mode doesn't matter for these workloads.** Across all 13
   settings with 4 vCPU, `npm ci` took 3.08 s to 3.60 s (65% to 75%),
   `git status` 0.179 s to 0.205 s, and the Go build 0.533 s to 0.613 s
   (72% to 82%), with no order by mode. None of them issues many
   flushes. The sync mode only shows where a program flushes after each
   write: 500 appends with `fsync` took 1.50 s to 2.05 s with full
   sync, 0.66 s to 1.22 s with the `fsync` mode, and 0.20 s to 0.49 s
   with no sync, against 1.28 s on the host.
2. **`npm ci` falls short even without a virtual disk.** On an APFS
   volume on a guest RAM disk it took 3.03 s, 77% of the host. The
   virtual disk costs 8% on top of that with the chosen setting. With 8
   vCPU it was slower (3.65 s), so the vCPU count isn't the cause
   either. Why the guest creates 17,000 files more slowly than the host
   is open (I97).
3. **ASIF and raw perform alike** (`npm ci` 3.46 s and 3.26 s with
   automatic caching and full sync). But a raw image gives space back to
   the host when the guest deletes data, and an ASIF image doesn't: 1
   GiB written and deleted in the guest left the ASIF image 1 GiB larger,
   and the raw image back where it started within 30 s. Over the
   benchmark, the 16 GB ASIF image grew to 11.7 GB on the host. That
   makes raw the choice for NFR03-footprint.

**Killing the VM mid-write lost nothing that the guest had flushed**, in
every mode tried. Six runs, on the raw image with automatic caching and
full sync, automatic caching and no sync, and host caching and no sync,
killed either this spike's `wb-vmd` stand-in or the Virtualization
service process that held the image. After each, `fsck_apfs` found the
volume clean, every acknowledged record was intact, and at most the one
file written after the last flush was missing. A killed process leaves
its writes in the host's memory, so this test can't tell the modes
apart. A host crash or power loss can, and for that Apple's header is
the source: with no sync, "the disk image cannot safely be reused on
failure", and the `fsync` mode is "best-effort" and doesn't flush the
drive's cache. Only full sync keeps flushed writes, which is what I44
decided Claude Code state and unpushed commits need.

## Measurements

Host: Apple M2 (4 performance and 4 efficiency cores), 16 GB, macOS
27.0.1, FileVault on, one Endpoint Security extension active (a file
sync client), load average 3 to 4 from other agents' work. Guest: macOS
27.0.1, 4 vCPU, 8 GiB, provisioned with `VZMacGuestProvisioningOptions`,
a 16 GB data disk formatted APFS in the guest with Spotlight off. Both
ran the same node 24.21, npm, Apple git 2.54 and Go 1.27.1 binaries.

Workloads, on the data disk in the guest and in `~/Library/Caches` on
the host, both excluded from Spotlight:

- `npm ci --offline`: the docs site's dependencies, 536 packages, 17k
  files, 317 MB, from a full npm cache on the same disk.
- `git status` in a shallow clone of nodejs/node v24.9.0: 43k files,
  814 MB.
- Go incremental build: `go build` of a gVisor-based program with its
  modules vendored, after changing one file. Also a clean build with an
  empty build cache.
- Two probes of the sync modes: 500 appends of 4 KiB, each followed by
  `fsync` (node's libuv issues `F_FULLFSYNC` on macOS), and 512 MiB of
  sequential writes with one `fsync` at the end.

Five warm runs per workload and setting, after one run to settle. Five
cold runs after `purge` in the guest, which drops the guest's file cache
(and with it the toolchain on the system disk). The host can't run
`purge` without a password, so it has no cold runs. The host ran the
workloads after each guest setting, with no VM running: 75 runs per
workload. Medians, warm:

| Setting | `npm ci` | `git status` | Go incremental | Go clean | 500 × `fsync` |
|---|---|---|---|---|---|
| Host | 2.33 s | 0.250 s | 0.439 s | 5.20 s | 1.28 s |
| raw, automatic, full | 3.26 s | 0.182 s | 0.542 s | 8.65 s | 2.05 s |
| raw, automatic, `fsync` | 3.44 s | 0.190 s | 0.540 s | 9.18 s | 0.66 s |
| raw, automatic, none | 3.31 s | 0.184 s | 0.545 s | 9.00 s | 0.49 s |
| raw, cached, none | 3.08 s | 0.193 s | 0.613 s | 9.75 s | 0.20 s |
| ASIF, automatic, full | 3.46 s | 0.194 s | 0.564 s | 9.11 s | 1.78 s |
| ASIF, cached, full | 3.60 s | 0.191 s | 0.607 s | 10.49 s | 1.65 s |
| ASIF, uncached, full | 3.33 s | 0.205 s | 0.605 s | 8.58 s | 1.50 s |
| ASIF, cached, none | 3.33 s | 0.185 s | 0.605 s | 11.59 s | 0.25 s |
| ASIF, uncached, none | 3.17 s | 0.193 s | 0.549 s | 9.76 s | 0.35 s |
| guest RAM disk | 3.03 s | 0.203 s | 0.564 s | 7.80 s | |
| raw, automatic, full, 8 vCPU | 3.65 s | 0.192 s | 0.575 s | 7.63 s | 1.94 s |

The other four ASIF settings, the cold runs, the sequential writes and
the spread of each set are in `results/stats.txt` on the spike branch.
Cold, with the chosen setting, the guest took 4.51 s for `npm ci`,
0.579 s for `git status` and 2.05 s for the Go build. There is no host
number to compare them with.

- **The host baseline is noisy and may be slow.** The host's 75
  `npm ci` runs spread from 2.29 s to 2.60 s. FileVault and the Endpoint
  Security extension add work to every file operation on the host and
  none in the guest, whose data volume isn't encrypted. That may be why
  `git status` is faster in the guest. A host without them would set a
  higher bar.
- **Throughput of the flush probe in the kill runs** (one record append
  with `F_FULLFSYNC` plus one 64 KiB file per step): 211 steps per
  second with full sync, 671 with no sync, 1,230 with host caching and
  no sync.
- **ASIF works with `VZDiskImageStorageDeviceAttachment`**, though the
  header of the macOS 27 SDK says only raw images are supported. Its
  advantage over a raw sparse file is moving between machines, which a
  data disk doesn't do.

## What was not measured

**A host crash or power loss.** Only a process kill was tested. The
settings for a host crash rest on Apple's headers
(`VZDiskImageStorageDeviceAttachment.h`, `VZDiskSynchronizationMode.h`).

**Cold runs on the host**, for want of `purge` without a password.

**Claude Code's own writes.** Whether Claude Code flushes its state
files, and how often, wasn't checked. If it flushes after each write,
full sync costs it about 4 ms per flush in the guest, against about 2.6
ms on the host.

**NVMe.** The data disk was a virtio block device. The NVMe controller
was not tried.

**Homes on a second disk** and their survival through a system disk
swap are X08-data-disk.

## What it means for the specs

- **S06-vm-lifecycle "Disks"** now names the setting: a raw sparse image,
  automatic caching, full sync. It drops "can be rebuilt from host state
  plus git, so it may use relaxed write-through settings", which I44
  overturned (changed in this pull request).
- **NFR02-fs-speed** is not met for `npm ci`, and its text is unchanged.
  The release gate (V1-M6-release-gate) measures it again. I97 looks
  for the cause in the guest. The open decision above says what to do
  until then.
- **I44** (data disk durability) gets the answer it waited on from this
  spike: full sync costs the NFR02-fs-speed workloads nothing measurable,
  so one fully synced data disk meets its target, and a second, relaxed
  disk for caches isn't needed for speed. Its other inputs, the mount
  rule and X08-data-disk, are unchanged.
- **NFR03-footprint**: a raw data image grows to the guest's peak use,
  and gives back what the guest deletes. An ASIF image keeps its peak.

## Spike code

Branch `spike/x5-fs-benchmark`, at
[`ec6ffea`](https://github.com/wraithbox/wraithbox/tree/ec6ffeadc854cfafb434657c05e0915f44a9260a/spikes/x05-fs-benchmark):
the `wb-vmd` stand-in with the data disk settings, the benchmark, the
kill and trim tests, and the raw results with `results/stats.txt`.

**Status:** Answered 2026-10-05: no. NFR02-fs-speed is not met for `npm ci` with any disk setting, and the data disk uses full sync
