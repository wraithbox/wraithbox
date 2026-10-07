# X20-shared-homebrew spike (throwaway)

Throwaway code for X20-shared-homebrew, never merged. The result is
`docs/spikes/X20-shared-homebrew.md` on `main`.

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), no `sudo`.
Guest: a fresh APFS clone of the X17-image-build spike's sealed bundle
`e5` (macOS 27.0.1, 4 vCPU, 4 GiB, 64 GiB sparse disk, `x17-guestd`
as the root LaunchDaemon on vsock port 1000), booted with Apple's NAT
network so Homebrew could reach `ghcr.io` directly. Not the egress
gateway: the spike measures Homebrew, not the proxy. Homebrew 7.0.8.

## Parts

- `host-vmd/`: the X17 `wb-vmd` stand-in, renamed `x20`, with a `serve`
  mode: it boots a bundle and relays each client of a Unix socket to a
  new vsock connection to the guest daemon.
- `req.py`: sends one request (`run`, `runs`, `put`, `hello`,
  `shutdown`) through that socket and logs it to `$X20_LOG`.
- `reloc.py`: reads Homebrew's formula API and prints, for the runtime
  closure of a Brewfile, which bottles pour outside `/opt/homebrew`.
- `brewfiles/`: project A (a polyglot project), project B (older
  versions of A's tools and mariadb), project C (mysql, which conflicts
  with mariadb).
- `guest/`: scripts `x17-guestd` runs as root.
  - `clt.sh`: Xcode command line tools from Apple's update servers.
  - `toolchain-user.sh`: the toolchain user `_wbtool` (uid below 500, no
    shell, no password, hidden) and `/opt/homebrew` owned by it.
  - `brew-install-shared.sh`: Homebrew's own installer, run as
    `_wbtool` through `sudo -u` with a clean environment.
  - `bundle-shared.sh`: a project's Brewfile into `/opt/homebrew` as
    `_wbtool`.
  - `project-users.sh`: two standard project users.
  - `probe-shared.sh`, `probe-shared-2.sh`: what a project user can and
    can't change in the shared prefix.
  - `brewfile-is-code.sh`: a Brewfile and a formula file run code as
    the toolchain user.
  - `bundle-per-user.sh`: option 2, a prefix in the project user's home
    (`~/homebrew`), which the project user owns.
  - `bundle-short-prefix.sh`: option 2b, a prefix the project user owns
    at a path no longer than `/opt/homebrew` (`/opt/wbp-c`), or a
    symbolic link there to a directory in the home (`REAL=`).
  - `probe-short-prefix.sh`: whether the relocated bottles still build
    a Python C extension and find their own files.
  - `noop-and-sizes.sh`: a reconcile with nothing to do, and disk use.
- `watch.sh`: stops the source build when the host's free disk drops
  below a floor or at a deadline.

## Run

```sh
S=.scratch
swift build -c release --package-path spikes/x20-shared-homebrew/host-vmd --scratch-path $S/x20-build
cp $S/x20-build/release/x20 $S/x20
codesign -f -s - --entitlements spikes/x20-shared-homebrew/host-vmd/x20.entitlements $S/x20
B=~/Library/Caches/wraithbox-spikes/x20
cp -c -R ~/Library/Caches/wraithbox-spikes/x17/e5 $B/g1
$S/x20 serve $B/g1 $B/g1.sock --nat > results/g1-serve.jsonl &
R="python3 spikes/x20-shared-homebrew/req.py $B/g1.sock"
G=spikes/x20-shared-homebrew/guest
$R run $G/clt.sh
$R run $G/toolchain-user.sh
$R run $G/brew-install-shared.sh
for x in a b c; do $R put spikes/x20-shared-homebrew/brewfiles/Brewfile.$x /private/tmp/Brewfile.$x 0644; done
$R run $G/bundle-shared.sh 3600 B=/private/tmp/Brewfile.a TAG=shared-a   # then b, c
$R run $G/project-users.sh
$R run $G/probe-shared.sh
$R run $G/brewfile-is-code.sh
$R run $G/bundle-per-user.sh 14400 P=wbp-b B=/private/tmp/Brewfile.a TAG=peruser-a &
spikes/x20-shared-homebrew/watch.sh $B/g1.sock wbp-b peruser-a 15 <deadline epoch>
$R run $G/bundle-short-prefix.sh 3600 P=wbp-c HP=/opt/wbp-c B=/private/tmp/Brewfile.a TAG=short-a
$R run $G/probe-short-prefix.sh 900 P=wbp-c HP=/opt/wbp-c
$R put spikes/x20-shared-homebrew/brewfiles/Brewfile.d /private/tmp/Brewfile.d 0644
$R run $G/bundle-short-prefix.sh 3600 P=wbp-d HP=/opt/wbp-d REAL=/Users/wbp-d/.hb B=/private/tmp/Brewfile.d TAG=symlink-d
$R run $G/noop-and-sizes.sh
$R shutdown
```

## Results

`results/g1-requests.jsonl` has every request and answer, with the
scripts' output. `results/g1-brew-logs.txt` has the pour, install,
source-build and error lines of each Homebrew run.
`results/g1-peruser-a-watch.log` has the progress of the source build,
which `watch.sh` stopped at its deadline. The symbolic link run
(`symlink-d`) was stopped by hand once it started building from source.

The bundle `~/Library/Caches/wraithbox-spikes/x20/g1` is a spike
artifact, and no product image may descend from it.
