# X31 - A root shell from a second guest daemon

**Purpose:** Whether `wb-guestroot`, a second root LaunchDaemon in a macOS guest, gives the host a root shell on a PTY over its own vsock port while `wb-guestd` is dead, stopped or restarting, and whether the two daemons' updates collide.

Brief: B448-admin-channel

## For review

- **Decides:** the answer to X31-admin-channel (I448), which
  S15-05-admin-path relies on, and two lines of spec text it adds:
  the admin shell has no Full Disk Access (S15-05-admin-path), and the
  two updates at one boot need no lock (S06-12-guest-agent).
- **You are approving:** the answer "yes, with conditions" and the
  measurements. The shell answered on every try while `wb-guestd` was
  booted out, stopped with `SIGSTOP`, and killed in a loop under
  launchd's throttle.
- **Controls touched:** none weakened. SEC08-proj-isolation gains a
  reason for the `AF_VSOCK` deny of S13-02-filesystem-profile, which
  it already has: without it, a standard user panicked the guest's
  kernel twice with a vsock connect to the guest's own CID. SEC11-root-gains-nothing is unchanged.
- **Assumed:** that a standard user without a Seatbelt profile stands
  in for a project user, as in X27-vsock-confinement, and that SSH with
  `sudo` stands in for `sudo` at a console. A Remote Login session has
  Full Disk Access in these guests, and a console session may not.
- **Open decisions:** whether `wb-guestroot` should get Full Disk
  Access. The spec states that it has none, which is the stricter
  choice. The brief holds the options.
- **Brief:** B448-admin-channel

## Question

From X00-index: can `wb-guestroot`, a root LaunchDaemon apart from
`wb-guestd` on a vsock port of its own below 1024, give the host a root
shell on a PTY while `wb-guestd` is dead, stopped or restarting? Can a
project user bind its port while launchd restarts it? Do the two
daemons' installs at one boot (S06-12-guest-agent, "Update at start")
collide? I448 adds: does the root shell run `dsmemberutil`,
`sysadminctl`, `mount -u` and `diskutil` as `sudo` does?

## Answer

**Yes, with conditions.** The second daemon answers whatever state
`wb-guestd` is in, and a standard user can't take its port. The
conditions are what the shell can't do and what the guest's kernel
does when vsock is open to project users.

1. **The shell answers while `wb-guestd` is down: yes.** The stand-in
   accepted the host's connection on port 1022 and started
   `/bin/zsh -l` as root (uid 0, `/dev/ttys000`) in each state:

   | `wb-guestd` | Port 1023 (`wb-guestd`) | Port 1022 (the stand-in) |
   |---|---|---|
   | running | answers | shell |
   | `launchctl bootout` | `ECONNRESET` | shell |
   | stopped with `SIGSTOP` (`Ts`) | connects, sends nothing in 1 s | shell |
   | killed whenever it runs, 40 s, 4 kills under launchd's throttle | 44 of 44 tries `ECONNRESET` | 44 of 44 tries answer, shell |

2. **A standard user can't bind the port: yes.** As the standard user
   `proj1`, every `bind` of port 1022 failed with `EACCES`: 2902 tries
   in 8 s while root killed the stand-in and launchd restarted it,
   2883 tries in 5 s while it was booted out, and 6861 tries in 15 s
   across another restart. Root bound the free port on its first try,
   and `proj1` bound port 5022 on its first. That is the rule of
   X27-vsock-confinement. After `kill -9`, launchd ran the new
   stand-in within 0.5 s in 1 run, and by the first 100 ms check in 3
   of 3 more.
3. **Condition: the guest's kernel panics on a project user's vsock
   connect.** From inside the guest, `proj1`'s connects to port 1022
   failed for CID 1 (`EFAULT`) and CID 2, the host, with no listener (`EAGAIN`). A
   connect to CID 3, the guest's own, hung. Within seconds the guest's
   kernel panicked with "corrupt list", panicked task the probe, 2
   times in 2 tries (12:49:22 and 12:51:35 guest time), and the guest
   rebooted. The second time, the hung probe was killed 3 s after its
   connect began. A standard user can so stop a VM. The
   session profile already denies `AF_VSOCK` (S13-02-filesystem-profile),
   which keeps project users off this path. The panic was found by
   chance and not narrowed down. I615 tracks it.
4. **Condition: the shell has no Full Disk Access.** TCC treats the
   daemon as the responsible process, and the guest's TCC database
   holds `wb-guestroot` denied for `kTCCServiceSystemPolicyAllFiles`,
   with `sshd-keygen-wrapper` allowed. SIP is on, so root can't change
   that from inside. Compared with `sudo` over SSH:

   | Command, as root | Admin shell | `sudo` over SSH |
   |---|---|---|
   | `dsmemberutil checkmembership -U proj1 -G admin` | works | works |
   | `sysadminctl -addUser` | works, logs `-14120` | works |
   | `sysadminctl -deleteUser`, `dscl . -delete /Users/<u>` | `eDSPermissionError` (`-14120`), record stays | works |
   | `ls` the TCC directory in `/Library/Application Support` | `Operation not permitted` | works |
   | `diskutil list`, `diskutil info`, `diskutil apfs list` | works | works |
   | `mount -u -o nobrowse` on the read-only tools disk | `Permission denied` | `Permission denied` |
   | `mount -u -o rdonly`, then `-o rw`, on an attached image | `EINVAL`, then works | `EINVAL`, then works |

   The four commands I448 names work as under `sudo`, except deleting
   a user. The shell works for the root checks it is for, such as the
   data mount cases, and not for whatever TCC guards. That matches
   I599, where `wb-guestd`, a root LaunchDaemon too, couldn't write in
   `/private/var/at`.
5. **The two updates don't collide, and the split is enough: yes.** At
   a real boot with a tools disk of a new version, the stand-in
   installed itself at 12:44:09.788 and `wb-guestd` installed itself at
   12:44:09.970, both from the product's code, both reached the new
   version, and neither refused. In 400 rounds where both ran at the
   same instant, no temporary file was left. With the directory there,
   as at every boot, both installed in 200 of 200 rounds. With it
   missing, both tried to make it, and one refused with
   `guestd-install-dir-failed` in 146 of 200 rounds and served as it
   was. That refusal fails closed, and at a boot the directory exists,
   since launchd runs both daemons from it. So no lock is needed.

## Measurements

Host: Apple M6, 24 GB, macOS 27.2 (26B123), Go 1.27.3,
`golang.org/x/sys` 0.49.0, `github.com/creack/pty` 1.1.24. Guest:
macOS 27.2 (26B123), 4 vCPU, 4 GiB, SIP on, a bundle from
`mise run vm:install` that `vm:provision` provisioned, booted by the
spike's Swift `wb-vmd` stand-in with a NAT network for SSH, a
`VZVirtioSocketDevice`, and the tools disk attached read-only. The
host opened every connection. In the provisioning boot, `wb-guestd`
(this checkout's code, version `0.0.0-x31.1`) and the stand-in were
installed as root LaunchDaemons with `KeepAlive`. The stand-in ran the
product's update code with the name `wb-guestroot`, listened on port
1022, refused any CID but 2, and relayed `/bin/zsh -l` on a PTY. The
tools disk held version `0.0.0-x31.2` of both.

- Without a vsock device, in the provisioning boot, both daemons
  failed `socket(AF_VSOCK)` with `ENODEV` and launchd restarted them.
- In the first boot, after one `kill -9`, launchd hadn't started a new
  stand-in 9 s later, and a `launchctl bootstrap` started one that
  logged nothing. That boot panicked 25 s later (Answer, item 3). In
  the next boots, the 4 restarts were at once (Answer, item 2). The first boot's
  behavior isn't explained.

## What was not measured

- **A console session.** The guest has no one logged in, and `sudo`
  ran over SSH, which has Full Disk Access.
- **Whether Full Disk Access can be given to `wb-guestroot`.** SIP
  keeps root from writing the TCC database. A configuration profile
  or the System Settings dialog weren't tried.
- **The panic's cause.** Only the connect to the guest's own CID was
  seen to hang and then panic. Root's connect to it wasn't tried after
  the second panic, and the hang wasn't checked without the kill.
- **The real `wb-guestroot`.** The stand-in isn't a gRPC service and
  has no version check with `wb-hostd`.
- **An update that has to replace a running `wb-guestd`.** Both
  daemons were at `0.0.0-x31.1`, and each replaced only its own file.

## What it means for the specs

- **S15-05-admin-path**: the line that said X31-admin-channel measures
  whether `wb-guestroot` answers now cites the result. A new
  paragraph says the shell has no Full Disk Access, so what TCC
  guards fails in it (changed in this pull request).
- **S06-12-guest-agent**, "Update at start": the two updates at one
  boot need no lock, and why (changed in this pull request).
- **S13-02-filesystem-profile**: unchanged. The `AF_VSOCK` deny there
  also keeps project users off the kernel panic of item 3. I615
  asks for a threat entry and a report to Apple.

## Spike code

Branch `spike/x31-admin-channel`, at
[bc95cab](https://github.com/wraithbox/wraithbox/tree/bc95cabc2d784b1f97e5ff064aad25f52ebb28a1/spikes/x31-admin-channel):
the Swift `wb-vmd` stand-in with its control socket and relay, the Go
`wb-guestroot` stand-in, probe and race tool, the guest step scripts,
the host scripts that type into the admin shell, and the raw results in
`results/`, listed in `results/README.md`.

**Status:** Answered 2026-11-08: yes, with conditions
