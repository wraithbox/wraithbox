# X27 - vsock confinement of project users

**Purpose:** Whether a project user's process in a macOS guest can open `AF_VSOCK` and take `wb-guestd`'s port, and whether the Seatbelt profile of S13-guest-confinement can stop it.

Brief: B101-vsock-confinement

## For review

- **Decides:** the answer to X27-vsock-confinement (I101), and what
  S04-architecture, S06-vm-lifecycle and S13-guest-confinement now
  state: `wb-guestd` listens on a vsock port below 1024, which only
  guest root can bind, and the session profile denies creating
  `AF_VSOCK` sockets with one named rule.
- **You are approving:** the answer "yes, with conditions", the
  measurements, and the spec text in this pull request. Without the
  conditions, a project user can pose as `wb-guestd`: in the spike, a
  standard user took port 1024 while launchd restarted the daemon and
  answered the host in its place for 21.7 s.
- **Controls touched:** none weakened. SEC08-proj-isolation and
  SEC10-audit gain the port rule, which the guest kernel enforces for
  every non-root process, also outside the Seatbelt profile. The
  Seatbelt rule is the second layer. SEC11-root-gains-nothing is
  unchanged: guest root can still bind the port, as it can already
  pose as any project user (T11-shared-vm-grants).
- **Assumed:** that the guest kernel's rule for ports below 1024 stays
  in later macOS releases. It isn't documented, and the conformance
  suite of S11-verification-and-spikes should test it on each guest
  image (I101 follow-up below).
- **Open decisions:** none.
- **Brief:** B101-vsock-confinement

## Question

From X00-index: can a non-root process in a macOS guest open
`AF_VSOCK`, and bind or listen on `wb-guestd`'s port while `wb-guestd`
is running or being restarted? Can the Seatbelt profile of
S13-guest-confinement deny `AF_VSOCK` to project users? I101 adds:
measure how long `wb-guestd`'s port is free when launchd restarts it
after a kill.

## Answer

**Yes, with conditions.** A project user can pose as `wb-guestd` when
nothing stops it, and two controls stop it.

1. **A non-root process can open `AF_VSOCK`: yes.** A standard user
   (not an admin) and an admin user both create vsock stream sockets,
   read the guest's CID (3) with the ioctl, and connect to a host
   listener. Datagram sockets fail for everyone (`EPROTONOSUPPORT`).
2. **Ports below 1024 need root: yes.** `bind` on ports 1, 80, and 1023
   fails with `EACCES` for both non-root users and works for root.
   From 1024 up, any user binds any free port. An automatic port
   (`VMADDR_PORT_ANY`) is taken from 1025 up, for root too.
3. **While `wb-guestd` holds its port, nobody else gets it.** Every
   variant fails with `EADDRINUSE` for root and non-root alike: the
   wildcard CID, the guest's own CID, and `SO_REUSEADDR` with
   `SO_REUSEPORT`. Binding to the host's CID fails with
   `EADDRNOTAVAIL`.
4. **During a restart, a project user takes the port: yes, at 1024.**
   After `kill -9`, launchd started `wb-guestd`'s stand-in again and
   the host reached it 11.5 to 18.2 ms after its last answer from the
   old process (five runs, host connecting every 5 ms). A standard
   user that tried `bind` every millisecond got port 1024 first. It
   answered 410 of the host's connections as `wb-guestd` for 21.7 s.
   Meanwhile launchd restarted the real daemon every 10 s, and each
   start failed with `EADDRINUSE` and exited. After the squatter
   exited, nothing answered on the port for 8.2 s, until launchd's next
   try.
   On port 1023 the same squatter failed with `EACCES` on all 2330
   tries.
5. **Seatbelt can deny it: yes.** Under a profile with
   `(deny system-socket (socket-domain AF_VSOCK))`, `socket()` fails
   with `EPERM`, so no bind, listen, connect, or ioctl follows. The
   squatter, run under that profile, failed at once, and the host
   reached the restarted daemon 54 ms after the old one's last
   answer. The symbolic name and the
   number 40 work the same. The rule denied `socket()` both after and
   before an unconditional `(allow system-socket)` in the same profile.
6. **Other Seatbelt rules don't cover vsock creation.** `(deny
   network*)`, `network-bind` and `network-inbound` fail `bind` with
   `EPERM`, and `network-outbound` fails `connect`, but each leaves
   `socket()` and the CID ioctl working. A `(deny default)` profile
   without `system-socket` denies `socket()` too.
7. **The agent-safehouse profiles, as rendered at the pinned commit,
   deny vsock bind and connect but not `socket()`.** Their
   `10-system-runtime` module allows every `system-socket`, and
   `20-network` allows network operations only on IP addresses and
   the `mDNSResponder` socket. With
   the rule of 5 added at the end, `socket()` fails too.

A guest process can't reach `wb-guestd` through its own CID either:
`connect` to the guest's own CID returned success while the daemon
listened, but the daemon's `accept` failed with `ECONNABORTED`, so the
daemon didn't get a connection from it. With nothing listening, `connect` failed
with `EINVAL`.

The conditions:

1. **`wb-guestd` listens on a vsock port below 1024.** Only guest root
   can bind it, so no project process can take it while `wb-guestd`
   restarts, also outside the Seatbelt profile: S13-guest-confinement
   lists processes started through LaunchServices and `launchd` as
   running outside the profile. `wb-hostd` connects only to that port.
2. **The session profile denies `AF_VSOCK` sockets** with
   `(deny system-socket (socket-domain AF_VSOCK))`, so a project
   process can't use vsock at all. That keeps it off every other guest
   port the host might connect to later.
3. **`wb-guestd` keeps listening after a failed `accept`.** An `ECONNABORTED` from
   `accept` is logged and skipped, never fatal, so a guest process that
   connects to the guest's own CID can't stop the listener.
4. **The guest kernel's port rule is tested on every image.** The
   conformance suite of S11-verification-and-spikes checks, as a
   project user, that binding `wb-guestd`'s port fails, and that the
   session profile denies `socket(AF_VSOCK)`.

## Measurements

Host: Apple M2, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0 (Swift 6.4),
Go 1.27.1, `golang.org/x/sys` 0.48.0. Guest: macOS 27.0.1 (26A434),
4 vCPU, 4 GiB, SIP on, a fresh clone of X18-vsock-handoff's installed
bundle, with a NAT network for SSH and a `VZVirtioSocketDevice`. The
`wb-guestd` stand-in ran as a root LaunchDaemon with `KeepAlive`. The
project user stand-in was a standard user (uid 502, not in `admin`).
Every call ran without a Seatbelt profile unless a profile is named.

| Call, as the project user | No profile | `deny system-socket` AF_VSOCK | `deny network*` | safehouse v0.12.0 |
|---|---|---|---|---|
| `socket(AF_VSOCK, SOCK_STREAM)` | ok | `EPERM` | ok | ok |
| CID ioctl | ok (3) | (no socket) | ok | ok |
| `bind` port 1023 | `EACCES` | (no socket) | `EPERM` | `EPERM` |
| `bind` port 1024, free | ok | (no socket) | `EPERM` | `EPERM` |
| `bind` port 1024, held by root | `EADDRINUSE` | not run | not run | not run |
| `connect` host listener | ok | (no socket) | `EPERM` | `EPERM` |

| Restart of the `wb-guestd` stand-in after `kill -9` | Result |
|---|---|
| Host sees the new process, nobody squatting (5 runs) | 11.5, 11.7, 12.6, 13.3, 18.2 ms after the old one's last answer |
| Project user squatting port 1024 | won after 3.2 s of trying, 2532 tries, then answered the host 410 times over 21.7 s |
| Real daemon's restarts while squatted | 3, 10 s apart, each `EADDRINUSE` |
| Project user squatting under the `deny system-socket` rule | `EPERM` on the first `socket()`; host reached the new daemon 54 ms after the old one's last answer, polling every 50 ms |
| Project user squatting port 1023 for 3 s, nothing bound | `EACCES` on all 2330 tries |

- The host's connection attempts while the port was free failed with
  `ECONNRESET` (`NSPOSIXErrorDomain` 54) from Virtualization.
- A guest connection to a host port with no listener failed with
  `EAGAIN` rather than `ECONNREFUSED`.
- `sysctl` doesn't list a vsock setting for the port rule, only
  `net.vsock.sendspace`, `recvspace`, and `pcbcount`.

## What was not measured

- **Older or newer guests.** Only macOS 27.0.1 ran. The port rule isn't
  in Apple's documentation, so condition 4 tests it per image.
- **`sandbox_init` from `wb-guestd`.** The spike applied profiles with
  `sandbox-exec`, which uses the same kernel policy. Its children
  inherit the profile, as S13-guest-confinement states.
- **The full session profile.** Only the agent-safehouse modules that
  its renderer selects by default ran, not the Wraith Box profile,
  which the Layer 2 work of S13-guest-confinement generates.
- **A process started through LaunchServices or `launchd`** as the
  project user. Condition 1 covers it without the profile, which the
  results above show for a process without one.

## What it means for the specs

- **S04-architecture**, "The VM provider passes descriptors, not
  bytes": `wb-guestd`'s port is below 1024, `wb-hostd` connects only
  to it, and `wb-guestd` keeps listening after an aborted `accept`. The line that
  called this untested now cites this result (changed in this pull
  request).
- **S06-vm-lifecycle**, "`wb-guestd`": the LaunchDaemon listens on a
  vsock port below 1024 (changed in this pull request).
- **S13-guest-confinement**, Layer 2: the rule in the profile, why the
  generic network rules aren't enough, and that the port rule covers
  processes outside the profile (changed in this pull request).
- **S11-verification-and-spikes**: the conformance checks of
  condition 4 (changed in this pull request).

## Spike code

Branch `spike/x27-vsock-confinement`, at
[edf2f33](https://github.com/wraithbox/wraithbox/tree/edf2f3307a20da8f3c2a52d002b2cee671abe6a2/spikes/x27-vsock-confinement):
the Swift `wb-vmd` stand-in with its control socket, the Go
`wb-guestd` stand-in and probe, the provisioning script, the Seatbelt
profiles, the experiment script, and the raw results in `results/`
with who answered when in `results/gap-summary.txt`.

**Status:** Answered 2026-10-06: yes, with conditions
