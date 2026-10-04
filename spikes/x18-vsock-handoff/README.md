# X18-vsock-handoff spike (throwaway)

Throwaway code for X18-vsock-handoff, never merged. The result is
`docs/spikes/X18-vsock-handoff.md` on `main`.

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0,
Go 1.27.1. Guest: macOS 27.0.1 from
`UniversalMac_27.0.1_26A434_Restore.ipsw`, 4 vCPU, 4 GiB, a system disk
(64 GiB sparse) and an empty data disk (8 GiB sparse),
`VZVirtioSocketDevice`, `VZFileHandleNetworkDeviceAttachment` on a
`SOCK_DGRAM` socketpair, entropy, one display. Same configuration as
X02-warm-start.

## Parts

- `host-vmd/`: Swift, the `wb-vmd` stand-in. Installs, provisions,
  boots, saves and restores the VM, accepts vsock connections in both
  directions, and hands each descriptor to `hostrecv` over a Unix
  socket with `SCM_RIGHTS`. It never reads or writes a handed-off
  descriptor.
- `go/cmd/hostrecv/`: Go, the `wb-hostd` / `wb-netd` stand-in. Speaks
  gRPC over passed vsock descriptors and ICMPv6 over the passed
  network descriptor. Can stop `host-vmd` with `SIGSTOP` while it uses
  them (`stop-peer-and-check`).
- `go/cmd/x18-guestd/`: Go, the `wb-guestd` stand-in. A root
  LaunchDaemon in the guest, `AF_VSOCK` through `golang.org/x/sys/unix`
  (no cgo, no raw syscalls), gRPC on port 1024: `grpc.health.v1`, plus
  `x18.Guest/Echo`, `Events` (what the guest saw) and `Dial` (connect
  to the host on vsock and serve gRPC there too).
- `guest/setup.sh`: gets `x18-guestd` into a fresh guest. First boot
  with `VZMacGuestProvisioningOptions` (macOS 27: a user, Remote Login
  on) on a NAT network, then SSH, `sudo install`, `launchctl
  bootstrap`, shut down. Spike-only: the product path is
  X17-image-build, and the product guest has no NAT network.
- `series.sh`: N restores or cold boots, each from a fresh APFS clone.
  Waits while the host is locked before each restore, and records the
  lock state in `<out>.lock`.

## Build and run

```sh
S=../../.scratch
(cd host-vmd && swift build -c release --scratch-path ../$S/x18-build)
cp $S/x18-build/release/x18 $S/x18
codesign -f -s - --entitlements host-vmd/x18.entitlements $S/x18
(cd go && GOWORK=off go build -o ../$S/x18-guestd ./cmd/x18-guestd \
        && GOWORK=off go build -o ../$S/hostrecv ./cmd/hostrecv)
B=~/Library/Caches/wraithbox-spikes/x18
$S/x18 install $B/base ~/Library/Caches/wraithbox-spikes/restore-images/UniversalMac_27.0.1_26A434_Restore.ipsw 4
guest/setup.sh $S/x18 $B/base $S/x18-guestd $S
cp -c -R $B/base $B/provisioned
$S/hostrecv $S/recv.sock &
$S/x18 handoff $B/base $S/recv.sock > results/handoff-run2.jsonl
cp -c -R $B/provisioned $B/rbase
$S/x18 prepare $B/rbase $S/recv.sock 60
./series.sh restore $S/x18 $B/rbase $S/recv.sock 20 results/restore-4g.jsonl
./series.sh cold $S/x18 $B/provisioned $S/recv.sock 10 results/cold-4g.jsonl
python3 stats.py results/restore-4g.jsonl results/cold-4g.jsonl > results/stats.txt
```

## Results

- `results/handoff-run1.jsonl`, `handoff-run2.jsonl`: every hand-off
  step, one JSON line each. Run 1 had a guest daemon with gRPC's
  default 4 MiB message cap (the 8 MiB echo failed on that) and stopped
  at the second save (`saveMachineStateTo` refuses an existing file).
  Run 2 is the full sequence. `hostrecv-run2.log` is the Go side of
  run 2.
- `results/restore-4g.jsonl`: 20 restores from fresh clones.
  `restore-4g-failed.jsonl`: the first attempt at that series, 20
  restores that failed with "permission denied" while the host screen
  was locked; `restore-4g-failed-syslog.txt` has the Secure Enclave
  error from the system log.
- `results/cold-4g.jsonl`: 10 cold boots from fresh clones of the
  provisioned bundle (each also saves; those lines are `prepare-save`).
  `prepare-rbase.jsonl`: the cold boot and save of the state that the
  restore series used.
- `results/stats.txt`: p50 and p95. For `cold-4g.jsonl`, "20 runs"
  counts lines, two per boot.
- `results/halfclose.jsonl`: half-close in both directions on raw
  passed vsock descriptors (`x18 halfclose`, on a bundle provisioned
  from `pristine` with the guest daemon that has ports 1025 and 1026),
  with the guest's own record of each.
- `results/setup-run*.log`: the provisioning runs (run 3 is the
  half-close bundle).
