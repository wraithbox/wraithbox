# X02-warm-start spike (throwaway)

Throwaway code for X02-warm-start, never merged. The result is
`docs/spikes/X02-warm-start.md` on `main`.

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0.
Guest: macOS 27.0.1 from `UniversalMac_27.0.1_26A434_Restore.ipsw`,
freshly installed and sitting at Setup Assistant, 4 vCPU, 4 or 8 GiB,
a system disk (64 GiB sparse) and an empty data disk (8 GiB sparse),
`VZVirtioSocketDevice`, `VZFileHandleNetworkDeviceAttachment` on a
`SOCK_DGRAM` socketpair, entropy, one display.

No code of ours runs in the guest. Liveness is the guest kernel
answering an ICMPv6 echo to `ff02::1` sent into the file-handle
attachment (`Sources/x02/net.swift`). The vsock probe in `guest/` was
never installed: putting it into the guest needs a maintainer step.

## Build and run

```sh
swift build -c release --scratch-path ../../.scratch/x02-build
cp ../../.scratch/x02-build/release/x02 ../../.scratch/x02
codesign -f -s - --entitlements x02.entitlements ../../.scratch/x02
B=~/Library/Caches/wraithbox-spikes/x02
../../.scratch/x02 install $B/base ~/Library/Caches/wraithbox-spikes/restore-images/UniversalMac_27.0.1_26A434_Restore.ipsw 4
../../.scratch/x02 validate $B/base
../../.scratch/x02 cold $B/base 20 > results/cold-4g.jsonl
../../.scratch/x02 cycle $B/base 20 60 > results/cycle-4g.jsonl
./clone-tests.sh ../../.scratch/x02 results          # then PART2=1, PART3=1
./clone-series.sh ../../.scratch/x02 base 20 results/series-4g.jsonl
./cold-series.sh ../../.scratch/x02 base 20 results/cold-clone-4g.jsonl
sh all-stats.sh
```

`results/` holds the raw JSON lines and `results/stats.txt` the
p50/p95 summary.
