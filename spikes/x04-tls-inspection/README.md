# X04-tls-inspection spike (throwaway)

Throwaway code for X04-tls-inspection, never merged. The result is
`docs/spikes/X04-tls-inspection.md` on `main`.

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0
(27A266a), Go 1.27.1, no `sudo`. Guest: a fresh APFS clone of the
X17-image-build spike's sealed bundle `e5` (macOS 27.0.1, 4 vCPU, 4 GiB,
64 GiB sparse disk, `x17-guestd` as the root LaunchDaemon on vsock port
1000), booted with Apple's NAT network and a read-only virtio-fs share.
Not the egress gateway: a throwaway relay in the guest stands in for
`wb-proxyd`, and pf in the guest stands in for `wb-netd`.

## Parts

- `host-vmd/`: the X20 `wb-vmd` stand-in, renamed `x04`, with
  `--share <dir>` (read-only, macOS automount tag). The share carried the
  spike binaries and a tar of the host's Xcode.app.
- `go/cmd/x04-proxy/`: the relay. `gen-ca` makes a P-256 CA with the
  S09-policy-credentials-audit profile (no name constraints). `serve`
  reads the SNI, then relays unchanged (pass) or terminates TLS with a
  24-hour leaf for that one name from the current CA, opens TLS
  upstream with the client's ALPN list, and copies bytes (inspect). It
  logs one JSON line per connection, with the client's alert when the
  client rejects the leaf. Session tickets are off, so every
  connection is a full handshake.
- `go/cmd/x04-goclient/`, `swift/x04-swiftclient.swift`,
  `clients/loop.{js,py,rb}`, `clients/Loop.java`: one request per new
  connection, optionally in a loop (count, interval), one JSON line each.
- `guest/` (run as root by `x17-guestd`):
  - `clt.sh`, `toolchain-user.sh`, `brew-install-shared.sh`: from X20.
  - `xcode.sh`: unpacks Xcode.app into `/Applications`.
  - `tools.sh`: the Homebrew formulas under test, the project user
    `wbp-a`, and Claude Code with its native installer.
  - `versions.sh`: client versions.
  - `proxy-up.sh`: CAs `ca1` and `ca2`, the relay as `nobody` under
    launchd, and pf: TCP 443 of every process but `nobody` routed to the
    relay, UDP 443 dropped, IPv6 TCP 443 refused.
  - `prep.sh`: a venv with `requests` and a SwiftPM package.
  - `trust.sh`: `sys-add` and `sys-remove` (system keychain), `vars`
    (the trust variable files and `/etc/wraithbox/env`), `vars-off`.
  - `trust-probe.sh`: user domain trust settings, `trust-settings-import`,
    the certificate in the System keychain without trust settings, and
    a hand-written `/Library/Security/Trust Settings/Admin.plist`.
  - `trust-supported.sh`: the authorization rights for trust settings,
    user domain trust as the project user, and `profiles install`.
    Earlier versions of this file (named `find-ts.sh` then) listed the
    trustd files and the `authorizationdb` rules; their text and output
    are in `results/g1-requests.jsonl`.
  - `matrix.sh`: every client once as `wbp-a`, with the variables of
    `/etc/wraithbox/env` or only one of them (`ONLYVAR`).
  - `reread.sh`: long-running clients across a CA install and a switch.
  - `extras.sh`: a SwiftPM binary target, and a pass-mode host with
    `SSL_CERT_FILE` with and without the public roots.
  - `pinning-rules.sh`: Apple's pinning rules from trustd's database.
  - `diag.sh`: relay and pf state.
- `r.sh`, `req.py`, `fetch.sh`, `show.py`, `singles.sh`: host-side
  helpers. `show.py` prints a matrix file, one line per client.

## Run

```sh
S=.scratch
swift build -c release --package-path spikes/x04-tls-inspection/host-vmd --scratch-path $S/x04-build
cp $S/x04-build/release/x04 $S/x04
codesign -f -s - --entitlements spikes/x04-tls-inspection/host-vmd/x04.entitlements $S/x04
(cd spikes/x04-tls-inspection/go && GOWORK=off go build -o ../../../$S/ ./cmd/...)
swiftc -O -o $S/x04-swiftclient spikes/x04-tls-inspection/swift/x04-swiftclient.swift
B=~/Library/Caches/wraithbox-spikes/x04
cp -c -R ~/Library/Caches/wraithbox-spikes/x17/e5 $B/g1
cp $S/x04-proxy $S/x04-goclient $S/x04-swiftclient $B/share/
cp -R spikes/x04-tls-inspection/clients $B/share/
tar -cf $B/share/Xcode.tar -C /Applications Xcode.app
$S/x04 serve $B/g1 $B/g1.sock --nat --share $B/share > results/g1-serve.jsonl &
R=spikes/x04-tls-inspection/r.sh
G=spikes/x04-tls-inspection/guest
$R run $G/clt.sh 3600
$R run $G/xcode.sh 3600
$R run $G/toolchain-user.sh 300
$R run $G/brew-install-shared.sh 1800
$R run $G/tools.sh 3600
$R run $G/proxy-up.sh 120
$R run $G/prep.sh 600
$R run $G/matrix.sh 3600 COND=none
$R run $G/trust.sh 120 MODE=vars CAS=ca1
$R run $G/matrix.sh 3600 COND=vars
$R run $G/trust.sh 120 MODE=sys-add CA=ca1        # refused
$R run $G/trust-probe.sh 120
$R run $G/trust-supported.sh 60
$R run $G/matrix.sh 3600 COND=only-SSL_CERT_FILE ONLYVAR=SSL_CERT_FILE
sh spikes/x04-tls-inspection/singles.sh
$R put $G/trust.sh /private/tmp/x04/trust.sh 0755
$R run $G/reread.sh 600
$R run $G/extras.sh 300
$R run $G/pinning-rules.sh 60
$R shutdown
```

## Results

- `g1-requests.jsonl`: every request to the guest and its answer, with
  the scripts' text and output.
- `matrix-*.jsonl`: one line per client and condition: exit status,
  seconds, the relay's view (SNI, mode, result, the client's alert), and
  the tail of the client's output.
- `proxy.jsonl`: the relay's log of every connection.
- `pinning-rules.txt`: trustd's pinning rules in the guest.
- `reread.txt`, `extras.txt`: the output of `reread.sh` and `extras.sh`.
- The bundle `~/Library/Caches/wraithbox-spikes/x04/g1` is a spike
  artifact, and no product image may descend from it.
