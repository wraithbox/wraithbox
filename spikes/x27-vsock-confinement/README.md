# X27-vsock-confinement spike (throwaway)

Throwaway code for X27-vsock-confinement (#101), never merged. The result
is `docs/spikes/X27-vsock-confinement.md` on `main`. Built on the
X18-vsock-handoff harness (branch `spike/x18-vsock-handoff`).

Question: can a non-root process in a macOS guest open `AF_VSOCK`, and
bind or listen on `wb-guestd`'s port while `wb-guestd` runs or restarts?
Can a Seatbelt profile deny `AF_VSOCK` to it?

Host: Apple M2, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0 (Swift 6.4),
Go 1.27.1, `golang.org/x/sys` 0.48.0. Guest: macOS 27.0.1 (26A434),
4 vCPU, 4 GiB, SIP on, a fresh APFS clone of the X18 `pristine` bundle
(installed, never booted) with a new MAC, NAT network (for SSH only) and
a `VZVirtioSocketDevice`.

## Parts

- `host-vmd/`: Swift, the `wb-vmd` stand-in. `x27 run` boots the bundle
  (first boot with `VZMacGuestProvisioningOptions`: admin user `x27`,
  Remote Login on), registers a host vsock listener on port 2048 (the
  product has none; it is here to see whether a guest process can dial
  the host), and serves a control socket: `connect <port>` and
  `poll <port> <intervalMs> <durMs>` open a connection to the guest port
  and record the first line the guest process sends, which names who
  answered.
- `go/cmd/x27-guestd/`: the `wb-guestd` stand-in. Root LaunchDaemon,
  `KeepAlive`, on vsock port 1024. Answers `guestd pid=… uid=0` and
  closes. Exits 1 when `bind` fails, like a real daemon.
- `go/cmd/x27-probe/`: what a guest process can do. `matrix` tries
  `socket`, `bind`, `listen` on ports 1, 80, 1023, 1024, 1025, 4096,
  50000, bind variants on 1024 (local CID, `SO_REUSEADDR` +
  `SO_REUSEPORT`, host CID, any port), and `connect` to the host and
  to the guest's own CID. `squat <port> <s>` tries to bind every
  millisecond and, once it has the port, answers every connection with
  `IMPOSTOR uid=…`.
- `guest/setup.sh`: clone, boot, provision, install `x27-guestd`,
  create the standard (non-admin) user `proj1` (uid 502), the project
  user stand-in, copy the probe and profiles in.
- `guest/profiles/`: Seatbelt profiles. `p*` are minimal ones, one
  rule each. `s1-safehouse.sb` is agent-safehouse v0.12.0 (commit
  `6bded066`, Apache-2.0) as its own renderer emits it with the default
  selection, `s2-safehouse-deny-vsock.sb` is s1 plus
  `(deny system-socket (socket-domain AF_VSOCK))`. `guest/safehouse.sh`
  renders both.
- `run.sh`: the experiments (`matrix`, `push`, `profiles`, `gap`,
  `squat`, `squat-sb`). `gap.py`: who answered the host, over time.
- `guest/ssh.sh`: run a command in the guest, as root with `-r`.

## Build and run

```sh
S=../../.scratch
(cd host-vmd && swift build -c release --scratch-path ../$S/x27-build)
cp $S/x27-build/release/x27 $S/x27
codesign -f -s - --entitlements host-vmd/x27.entitlements $S/x27
(cd go && GOWORK=off go build -o ../$S/x27-guestd ./cmd/x27-guestd \
        && GOWORK=off go build -o ../$S/x27-probe ./cmd/x27-probe)
guest/safehouse.sh <agent-safehouse checkout at 6bded066> guest/profiles
guest/setup.sh $S/x27 ~/Library/Caches/wraithbox-spikes/x18/pristine \
  ~/Library/Caches/wraithbox-spikes/x27/run1 $S
./run.sh $S results matrix push profiles gap squat squat-sb
guest/ssh.sh $S -r "sudo -u proj1 /usr/local/bin/x27-probe squat 1023 3 proj1-port1023" \
  > results/squat-1023-guest.jsonl
python3 gap.py results/gap-host.jsonl results/squat-host.jsonl \
  results/squat-sb-host.jsonl > results/gap-summary.txt
```

## Results

- `matrix.jsonl`: the probe matrix as root, `x27` (admin, uid 501) and
  `proj1` (uid 502), with `x27-guestd` running and then stopped.
  `matrix-host-sanity.jsonl`: the host reaching `x27-guestd`.
- `profiles.jsonl`: the matrix as `proj1` under each profile, with
  `x27-guestd` stopped, so only the profile and the port rules stand in
  the way.
- `gap-*`: five `kill -9` of `x27-guestd` while the host connects every
  5 ms. `gap-summary.txt` has who answered when.
- `squat-*`: `proj1` squats port 1024 while root kills `x27-guestd`,
  the host connects every 50 ms. `squat-sb-*`: the same, `proj1` under
  `p2-deny-socket-domain-40.sb`. `squat-1023-guest.jsonl`: `proj1`
  tries port 1023 for 3 s with nothing bound to it.
- `setup.log`, `vm-events.jsonl`: provisioning, and every connection a
  guest process opened to the host listener.
