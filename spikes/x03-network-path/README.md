# X03-network-path spike (throwaway)

Throwaway code for X03-network-path, never merged. The result is
`docs/spikes/X03-network-path.md` on `main`.

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0,
Go 1.27.1, gVisor `go` branch at `v0.0.0-20261004063249-f57b8fc79db4`.
Guest: macOS 27.0.1, 4 vCPU, 4 GiB, an APFS clone of the X18-vsock-handoff
`pristine` bundle (installed, never booted), provisioned by this spike.

The VM under test has one NIC, `VZFileHandleNetworkDeviceAttachment` on a
`SOCK_DGRAM` socket pair. No NAT attachment and no shared directory, in
any boot, including the provisioning boot.

## Parts

- `host-vmd/`: Swift, the `wb-vmd` stand-in, signed ad hoc with only
  `com.apple.security.virtualization`. Boots the bundle, passes the host
  end of the socket pair to `x03-netd` with `SCM_RIGHTS` and closes its
  own copy (X18-vsock-handoff), and sets the attachment's MTU.
  `--provision` is the first boot with `VZMacGuestProvisioningOptions`
  (a user, Remote Login on).
- `go/cmd/x03-netd/`: Go, the `wb-netd` stand-in. gVisor `pkg/tcpip` on
  a `channel` endpoint wrapped in `ethernet`, pumped to and from the
  socket pair, one frame per datagram. A link filter in front of the
  stack (guest MAC, IPv4 and ARP only, guest source address, ARP and
  ICMP for the gateway only, UDP only to the gateway's port 53 and DHCP),
  a DHCP server, DNS on UDP and TCP port 53 with a hard-coded allowlist
  and synthetic addresses from 198.18.0.0/15, and a TCP forwarder that
  accepts only synthetic addresses on ports 80 and 443 and relays them
  to a stub over a Unix socket. The stub (the `wb-proxyd` stand-in)
  runs in the same process and serves `/1g` from a file and `/sink`.
  `zero.test` is answered from memory inside `x03-netd`, to measure the
  stack without the stub. A host-only forward 127.0.0.1:2222 to the
  guest's port 22 lets the spike drive the guest over SSH; the product
  has no such path.
- `guest/probe.sh`: runs in the guest and tries each path
  S07-egress-gateway says must work or fail.
- `run.sh`: starts `x03-netd`, then `x03`, logs to `results/`.
- `gssh.sh`: SSH into the guest through the forward. `gstop.sh`: shut
  it down.
- `bulk-cpu.sh`: 1 GiB downloads (or uploads, to `/sink`) in the guest,
  with the CPU time of `x03-netd` and of Virtualization's service
  process for each.
- `summarize.py`: lease times, names resolved, drop counters.

## Build and run

```sh
S=../../.scratch
B=~/Library/Caches/wraithbox-spikes/x03
(cd host-vmd && swift build -c release --scratch-path ../$S/x03-build)
cp $S/x03-build/release/x03 $S/x03
codesign -f -s - --entitlements host-vmd/x03.entitlements $S/x03
(cd go && GOWORK=off go build -o ../$S/x03-netd ./cmd/x03-netd)
mkfile 1g $S/bulk-1g.bin
umask 077; openssl rand -hex 12 > $S/guest-pass
cp -c -R ~/Library/Caches/wraithbox-spikes/x18/pristine $B/base
# provisioning boot, also on the file-handle NIC only; shut down over SSH
$S/x03-netd -sock $S/netd.sock -mac <mac> -stub $S/stub.sock -stub-file $S/bulk-1g.bin &
$S/x03 run $B/base $S/netd.sock 20 --provision x03 $S/guest-pass
cp -c -R $B/base $B/run1
./run.sh $S run1-mtu1500 $B/run1 40 1500 &
./gssh.sh $S 'cat > /tmp/probe.sh; sh /tmp/probe.sh <host LAN IP> 3' < guest/probe.sh
./bulk-cpu.sh $S 3
./run.sh $S run3-mtu65535 $B/run1 30 65535 -allow bulk.test,allowed.test,zero.test &
./bulk-cpu.sh $S 3 http://zero.test/
./run.sh $S run6-idle-hour $B/idle 61 1500 -ssh ''   # no SSH: an idle guest
python3 summarize.py names results/run6-idle-hour-netd.jsonl
```

## Results

Every `*-netd.jsonl` is `x03-netd`'s log: one JSON line per decision,
with the rule. Every `*-vmd.jsonl` is `x03`'s.

- `provision-*`: the provisioning boot.
- `run1-mtu1500-*`: probe (`run1-mtu1500-probe.txt`), downloads with
  CPU time, Claude Code started with a placeholder key and an empty
  allowlist (`run1-claude-p.txt`).
- `run2-mtu65535-*`, `run3-mtu65535-*`, `run4-mtu9000-*`,
  `run5-mtu1500-*`: downloads at each MTU, from the stub and from
  memory (`zero`).
- `run5-mtu1500-learn-*`: every name resolves (a learn-mode stand-in),
  then Claude Code again (`run5-claude-learn.txt`).
- `run6-idle-hour-*`: an idle guest's first hour, no SSH, every name
  outside the allowlist refused.
- `run7-mtu1500-*`, `run10-mtu65535-*`: uploads of 1 GiB to the stub's
  `/sink` (`*-upload-cpu.txt`).
- `run8-mtu1500-relay1m-*`, `run9-mtu65535-relay1m-*`: downloads and
  uploads with a 1 MiB relay buffer instead of `io.Copy`'s 32 KiB.
- `run11-mtu1500-unfiltered-*`: the ARP rule off (`-unfiltered`). The
  guest declines every lease and never gets an address.
- `run12-mtu1500-unfiltered-icmp-*`: the ICMP rule off
  (`-unfiltered-icmp`). Ping to other addresses still gets no answer.
- `entitlements.txt`: `codesign` of both binaries.
- `host-baseline.txt`: the same file from the stub on the host, over
  loopback TCP and over the stub's Unix socket.
