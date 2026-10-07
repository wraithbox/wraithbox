# X17-image-build spike (throwaway)

Throwaway code for X17-image-build, never merged. The result is
`docs/spikes/X17-image-build.md` on `main`.

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0,
Go 1.27.1, no passwordless `sudo` (and none used). Guest: macOS 27.0.1
from `UniversalMac_27.0.1_26A434_Restore.ipsw` (already downloaded),
4 vCPU, 4 GiB, one 64 GiB sparse system disk, `VZVirtioSocketDevice`, a
file-handle network attachment with nothing behind it (a NAT network only
for the provisioning boot), entropy, one display.

## Parts

- `host-vmd/`: Swift, the `wb-vmd` stand-in. `install` creates a bundle
  and installs macOS. `boot` cold boots a bundle (optionally with
  `VZMacGuestProvisioningOptions` and a NAT network), connects to vsock
  port 1000 until the guest daemon answers, sends a request file line by
  line and prints each answer as JSON.
- `go/cmd/x17-guestd/`: Go, the `wb-guestd` stand-in. A root
  LaunchDaemon that listens on vsock port 1000 and runs `hello`, `run`
  (a shell script as root), `put` and `shutdown`.
- `guest/datavol.sh`: attaches a stopped bundle's disk on the host as the
  user and mounts its Data volume.
- `guest/inject.sh`: writes files into the Data volume from the host.
  `direct` is the issue's candidate (LaunchDaemon plist, binary,
  `.AppleSetupDone`). `agent` and `home` try a LaunchAgent that runs
  `sudo` at the provisioning user's automatic login.
- `guest/provision-ssh.sh`: the path that works. First boot with
  `VZMacGuestProvisioningOptions` (user `wbprov`, Remote Login on) on a
  NAT network, one SSH session that installs `x17-guestd`, then
  everything else over vsock.
- `guest/scan.sh`: the sealing scan's account, remote access, login,
  sudoers, leftover and secret checks. Exits 1 on any finding.
- `guest/cleanup.sh`, `guest/disable.sh`: Remote Login off, automatic
  login off, try to delete the provisioning user, then disable it.
- `guest/report.sh`, `guest/diag-user.sh`, `guest/check-disabled.sh`,
  `guest/project-user.sh`, `guest/violate.sh`, `guest/guestlog.sh`:
  diagnostics.
- `mkreq.py` builds request files, `show.py` prints a result file.

## Build and run

```sh
S=.scratch                                  # at the repository root
(cd spikes/x17-image-build/host-vmd && swift build -c release --scratch-path ../../../$S/x17-build)
cp $S/x17-build/release/x17 $S/x17
codesign -f -s - --entitlements spikes/x17-image-build/host-vmd/x17.entitlements $S/x17
(cd spikes/x17-image-build/go && GOWORK=off go build -o ../../../$S/x17-guestd ./cmd/x17-guestd)
B=~/Library/Caches/wraithbox-spikes/x17
G=spikes/x17-image-build/guest
$S/x17 install $B/base ~/Library/Caches/wraithbox-spikes/restore-images/UniversalMac_27.0.1_26A434_Restore.ipsw

# e1: the issue's candidate, host writes as the user
cp -c -R $B/base $B/e1
$G/inject.sh direct $B/e1 $S/x17-guestd $B
$S/x17 boot $B/e1 req-hello.jsonl --timeout 150

# e5: provisioning boot, then the sealed image
openssl rand -hex 12 > $S/e5-pass
python3 spikes/x17-image-build/mkreq.py $S/req-e5.jsonl hello run:$G/scan.sh \
  runpw:$G/cleanup.sh:$S/e5-pass runpw:$G/disable.sh:$S/e5-pass run:$G/scan.sh shutdown
cp -c -R $B/base $B/e5
$G/provision-ssh.sh $S/x17 $B/e5 $S/x17-guestd $S $S/req-e5.jsonl results/e5-provision-boot.jsonl $S/e5-pass
$S/x17 boot $B/e5 $S/req-e5v.jsonl --timeout 200 > results/e5-sealed-boot.jsonl
```

## Results

- `install-1.*`: the install from the restore image.
- `e1-*`: host-written LaunchDaemon. The daemon never answered.
- `e2-*`: provisioning user with automatic login and a host-written
  LaunchAgent, in `/Library/LaunchAgents` (first boot) and in the user's
  own `~/Library/LaunchAgents` (second boot). Neither ran.
  `e2-launchd-x17.txt` has launchd's refusals.
- `e3-*`: the first SSH-provisioned run, with the first scan and the
  failed user removal. `e3-diag-user.jsonl` shows why.
- `e4-*`: removal refused again, then the user disabled and scanned.
- `e5-*`: the full pipeline on a fresh clone of the installed bundle,
  the sealed image's verification boot, and a project user created and
  deleted by `x17-guestd`.
- Passwords in the result files are replaced by `<redacted>`.
