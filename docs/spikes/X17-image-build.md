# X17 - Unattended image build

**Purpose:** Whether a macOS guest installed from an Apple restore image can get past Setup Assistant with `wb-guestd` running as a root LaunchDaemon, without SSH, scripted clicks or MDM, and how long a build takes.

Brief: B28-image-build

## For review

- **Decides:** the answer to X17-image-build (I28), and how
  `wb image build` gets `wb-guestd` into a new guest, which
  S06-vm-lifecycle, "Images", now states.
- **You are approving:** the answer "no", and the build that replaces
  the one S06-vm-lifecycle described, with a carve-out in
  S04-architecture for the provisioning password, which `wb-vmd` holds
  for a build VM's first start. The first boot uses the macOS 27
  provisioning options, which create a provisioning user and turn
  Remote Login on, and one SSH session as that user installs
  `wb-guestd`. Everything after that goes through `wb-guestd`. The
  build then turns Remote Login off and disables the provisioning user,
  because macOS refuses to delete it, and the sealing scan fails the
  build on either. A build takes about 6.6 minutes from a downloaded
  restore image.
- **Controls touched:** SEC12-least-privilege (no root on the host) is
  kept. The issue's candidate fails because of it, since an unprivileged
  host can't write the root-owned files launchd needs. SEC04-no-guest-secrets
  and SEC05-default-deny are kept by the sealing scan, which now fails
  the build on Remote Login, automatic login, `/etc/kcpassword`, the
  provisioning user unless it is disabled, and files a non-system user
  owns. The provisioning boot has a network the product VM doesn't
  have (open decision 2).
- **Assumed:** that the SSH session can run over the build VM's own
  file-handle network and the host's userspace stack instead of a NAT
  network. The spike used NAT for that one boot. That a disabled
  account that holds the volume's secure token gives a project user
  nothing (I142 measures it). The spike tried neither.
- **Open decisions:**
  1. The provisioning user stays in the image, disabled, because macOS
     refuses to delete the last secure token holder. The I28 comment of
     2026-10-04 asked for it to be removed. Recommended: accept the
     disabled user, which S06-vm-lifecycle already allowed ("disabled
     or removed"), and have the sealing scan check the properties that
     make it harmless. This rests on the secure-token assumption above
     (I142).
  2. Which network carries the one SSH session. Recommended: the build
     VM's file-handle network, with the host dialing the guest's port
     22 through its userspace stack and nothing else reachable, tried
     in I139. The alternative is a NAT network for the
     provisioning boot only, as in this spike, which gives the guest
     unfiltered internet before any project code has run. The spike's
     SSH client also didn't check the guest's host key
     (`StrictHostKeyChecking=no`), so nothing authenticated the guest
     end of that session.
  3. The provisioning options are new in macOS 27, so this build needs
     a macOS 27 host, and NFR04-host-platforms says macOS 15 or later.
     No build without SSH or clicks was found for older hosts.
     Recommended: raise NFR04-host-platforms to macOS 27 for v1 hosts.
     The alternative is to ship built images to older hosts, which
     S06-vm-lifecycle leaves for later. Tracked in I143.
- **Brief:** B28-image-build

## Question

From X00-index: can a macOS guest installed from an Apple restore image
get past Setup Assistant, with `wb-guestd` running as a root
LaunchDaemon, without SSH, scripted clicks, or MDM? The candidate was
writing files to the new guest's Data volume from the host at build
time, before any untrusted code has run. Measure the build time from
the restore image. The maintainer's comment on I28 adds: whatever
provisioning X17-image-build picks, show Remote Login off, the
provisioning user removed, and the sealing scan of S06-vm-lifecycle
extended to check for both (SEC04-no-guest-secrets,
SEC05-default-deny).

## Answer

**No.** A host without root can't get `wb-guestd` running as a root
LaunchDaemon by writing to the guest's disk, and no other way without
SSH that the spike tried worked. The build that works uses one SSH session. Each part, with
the evidence in "Measurements":

1. **The host can mount the Data volume as the user: yes.** After the
   install, `hdiutil attach` and `diskutil mount` of the bundle's disk
   worked without `sudo`. The Data volume wasn't encrypted. The mount
   ignores ownership, so the host could write anywhere on it.
2. **Every file the host writes belongs to the host user: verified.**
   New files and directories got the host user's uid (501) and group
   20, and `chown` to root failed with "Operation not permitted". uid
   501 is also the first account macOS creates in the guest, so a
   `wb-guestd` binary written this way would belong to whoever gets
   501 there.
3. **launchd refuses what the host writes: no daemon ran.**
   - A LaunchDaemon plist, the binary and `/var/db/.AppleSetupDone`,
     as the issue proposed: the guest booted and nothing answered on
     vsock in 150 s. At boot, `smd` bootstrapped the two plist files macOS
     ships in `/Library/LaunchDaemons` and skipped the one the host
     wrote. A second host-written daemon, which only touches a file,
     never ran either.
   - A LaunchAgent in `/Library/LaunchAgents`, with a provisioning user
     logged in automatically, meant to run `sudo` with a password the
     host left: launchd logged "Caller specified a plist with bad
     ownership/permissions" for it, for every user session including
     the provisioning user's.
   - The same LaunchAgent in that user's own `~/Library/LaunchAgents`,
     owned by the user's uid: it didn't run, and launchd logged nothing
     about it. The spike didn't find out why.
   - With root on the host the files could be owned by root, but
     SEC12-least-privilege rules out root on the host, so the spike
     didn't try it.
4. **The provisioning options get past Setup Assistant: yes.**
   `VZMacGuestProvisioningOptions` (macOS 27 host and guest, as in
   X18-vsock-handoff) created the user `wbprov` with a password and
   Remote Login on. The guest wrote `/var/db/.AppleSetupDone` itself,
   owned by root. Later boots go straight to the login window, and no
   Setup Assistant process runs.
5. **One SSH session installs `wb-guestd`: yes.** SSH as `wbprov`
   answered 21 to 24 s after the start, and `sudo install` plus
   `launchctl bootstrap` put the test daemon in place as root. The host
   reached it over vsock 23 to 26 s after the start. From then on the
   spike talked to the guest over vsock only.
6. **Remote Login off: yes.** Through the test daemon,
   `launchctl bootout` and `launchctl disable` of `com.openssh.sshd`
   stopped sshd, and `systemsetup -getremotelogin` reports Off. It stays off
   after a reboot.
7. **The provisioning user removed: no.** `wbprov` holds the volume's
   only secure token and is its volume owner. `sysadminctl -deleteUser`
   refuses ("it's either last admin user or last secure token user"),
   `sysadminctl -secureTokenOff` refuses ("User cannot disable their own
   SEP credential"), and `dscl . -delete` fails with
   `eDSPermissionError`. Root also couldn't delete its home: `chmod -N`
   on `Desktop`, `Documents` and `Downloads`, which have an "everyone
   deny delete" ACL, failed with "Operation not permitted". The spike
   didn't find out whether Full Disk Access would allow it.
8. **The provisioning user disabled: yes.** The test daemon took
   `wbprov` out of `admin` and every other group it could, changed its
   password with `sysadminctl -resetPasswordFor` to a random one that
   nothing kept, disabled it with `pwpolicy -disableuser`, set its shell
   to `/usr/bin/false`, hid it, and removed its login keychain. After
   that, `pwpolicy -authentication-allowed` reports "account is
   disabled" and `dscl . -authonly` refuses the old password. It keeps
   the secure token. It stays a member of `_lpoperator` and a share point
   group, through nested groups every local user is in, and in
   `com.apple.access_disabled`.
9. **The sealing scan catches both: yes.** Run as root by the test
   daemon on the provisioned guest, it failed with 8 findings, among
   them `wbprov` in `admin` with a password, `com.openssh.sshd` enabled
   and loaded, a listener on TCP port 22, `wbprov`'s files outside its
   home, and its login keychain. After the cleanup it passed, and it
   passed again on the next boot of the sealed image.

The conditions of the build that works:

1. **The host runs macOS 27 or later**, for the provisioning options
   (open decision 3, I143).
2. **The first boot is the only one with SSH.** It uses the provisioning
   options with a random password that `wb-vmd` generates, hands to the
   build's SSH client and drops when the start call returns, and one
   SSH session that installs `wb-guestd` and nothing else. No product
   image descends from a bundle that skipped the later steps.
3. **All later provisioning goes through `wb-guestd`**, over vsock,
   starting with Remote Login off and automatic login off.
4. **The provisioning user is disabled, not removed** (open decision
   1). Its password is replaced with a random one that the build
   discards.
5. **The sealing scan runs in the guest, as root, through
   `wb-guestd`**, before any project code has run (I144 settles which
   user installs the base layer, and when). It fails the build on: an
   enabled account in `admin` or `wheel` other than root and the system
   accounts macOS ships in it; a non-system account other than one that
   is the volume owner, disabled, hidden, without a shell, and only in
   `staff`, `everyone`, `localaccounts`, `_lpoperator`,
   `com.apple.sharepoint.group.*` and `com.apple.access_disabled` (the
   group `pwpolicy` adds when it disables an account);
   Remote Login (sshd enabled or loaded, or a listener on port 22);
   screen sharing or remote management; automatic login or
   `/etc/kcpassword`; sudoers additions; files outside `/Users` owned by
   a non-system uid other than that user's temporary folders; launchd
   jobs outside an allowlist of what macOS ships (`com.apple.*` and
   `amsdstat.plist` in `/Library/LaunchDaemons`) plus `wb-guestd`'s; and
   the secrets the scan already looked for. S06-vm-lifecycle,
   "Sealing", adds checks the spike's scan didn't have: membership
   through `dsmemberutil`, `authorized_keys`, `sshd_config`, SSH host
   keys, the kept user's LaunchAgents, its secure token, and `IsHidden`.

   The spike's scan treated an account as enabled when its
   `AuthenticationAuthority` had a `ShadowHash` entry, which is what a
   password login needs. macOS ships `_mbsetupuser` in `admin` without
   one.
6. **The host never mounts a guest disk to build an image.** It doesn't
   need to, and the host then parses no guest-written filesystem.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0, Go
1.27.1, no `sudo`. Guest: macOS 27.0.1 from
`UniversalMac_27.0.1_26A434_Restore.ipsw` (26.6 GB, already
downloaded), 4 vCPU, 4 GiB, one 64 GiB sparse system disk,
`VZVirtioSocketDevice`, and a file-handle network attachment with
nothing behind it, or a NAT network for the provisioning boot. Each run
started from a fresh APFS clone of one installed bundle. The clock
starts when the boot tool starts the VM.

| Step | Time | Runs |
|---|---|---|
| Install from the local restore image | 271.6 s | 1 |
| Host mounts the Data volume, writes, unmounts | 2.2 to 3.5 s | 3 |
| Provisioning boot: SSH answers | 21 to 24 s | 3 |
| Provisioning boot: test daemon answers on vsock | 23.1 to 26.0 s | 3 |
| Sealing scan, in the guest | 19.4 to 32.2 s | 11 |
| Cleanup and disable requests, guest-side time | 40.7 s | 1 |
| Provisioning boot, start to guest stopped (full pipeline) | 127.1 s | 1 |
| Provisioned image, cold boot to the test daemon answering | 8.5 to 11.0 s (median 8.8 s) | 6 |

- **Build time:** 271.6 s to install and 127.1 s for the provisioning
  boot make 398.7 s, about 6.6 minutes, from a downloaded restore image
  to a sealed image. Of the 127.1 s, the two scans took 54.7 s, and the
  cleanup request took 38.7 s in the guest, with the deletion attempts
  that a product build skips and two `find` passes over the Data
  volume. The 40.7 s in the table is that request plus the disable
  request (2.0 s). A verification boot of the sealed image, start to
  guest stopped, took 35.6 s more.
  The restore image download was not measured (I49).
- **Cold boot:** after the provisioning boot, the daemon started 7.6 to 10.1 s after
  the guest kernel's boot time, with one disk. X18-vsock-handoff
  measured 10.9 s (p50) to its listener with two disks (I86).
- **Failed candidates:** the host-written daemon didn't answer in
  150 s (first boot) and the LaunchAgent variants didn't in 300 s each.
- **Project users:** the test daemon created a user with
  `sysadminctl -addUser` and `createhomedir`, and
  `sysadminctl -deleteUser` removed its home but not its record
  (`eDSPermissionError`), on the sealed image. `wb-guestd` removes
  project users (S06-vm-lifecycle), so that is filed as I138.

## What was not measured

- **Root on the host.** Writing root-owned files from the host needs
  `sudo`, which SEC12-least-privilege rules out at run time.
- **SSH without a NAT network** (open decision 2, I139).
- **Why the user's own LaunchAgent didn't run.** Unified log entries of
  the guest weren't readable on the host, and the spike stopped that
  path once SSH worked.
- **The base layer's tools.** Homebrew and the Xcode command line tools
  need the network through the egress gateway, which this spike didn't
  have.
- **The Network Extension approval** of S13-guest-confinement during
  the build, which waits for X14-flow-attribution.

## What it means for the specs

- **S06-vm-lifecycle**, "Images": "Built by Wraith Box" now describes
  the provisioning boot, the one SSH session, and the hand-over to
  `wb-guestd`, and says the host never mounts a guest disk to build an
  image. "Sealing" lists the checks of condition 5 (changed in this pull
  request). The rest of S06-vm-lifecycle is unchanged, because I46
  edits its session parts.
- **S04-architecture**: the `wb-vmd` row and "Secrets live in one
  process" name the provisioning password as the one secret `wb-vmd`
  holds, for a build VM until its first start returns (changed in this
  pull request).
- **NFR04-host-platforms** says macOS 15 or later, and this build
  needs macOS 27 (open decision 3). This pull request leaves it as it
  is until that is decided.
- **S11-verification-and-spikes**, "Spikes": the list of answered
  spikes should name X17-image-build. I50 edits that file in the same
  wave, so it's left to I140.
- **S10-tech-stack**: the install reads the restore image from a local
  file, so `wb-vmd` doesn't need a network for it. Who downloads it stays
  with I49.

## Spike code

Branch `spike/x17-image-build`, at
[6d3ff44](https://github.com/wraithbox/wraithbox/tree/6d3ff44df8f8e3cd5e9ec3ed2c0dd07dcdeac4c7/spikes/x17-image-build):
the Swift `wb-vmd` stand-in, the Go guest daemon, the host-side Data
volume scripts, the provisioning script, the scan and cleanup scripts,
and the raw results in `results/`, with passwords redacted. The
spike's bundles (`base` and `e1` to `e5`) are spike artifacts, and no
product image may descend from them.

**Status:** Answered 2026-10-07: no
