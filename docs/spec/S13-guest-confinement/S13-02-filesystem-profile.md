# S13-02 - Layer 2: Seatbelt Filesystem Profile (v1)

**Purpose:** The Seatbelt profile each session's process tree runs under, where
it comes from, its exceptions for Xcode builds, and its known gaps.

## Design

Each session's process tree runs under a Seatbelt profile
(`sandbox_init`). The kernel enforces it, children inherit it, and a
process cannot remove it, much like Landlock.

### Profile

Reads are denied by default, with an allowlist for the
system, Homebrew, Xcode, the trust files in `/etc/wraithbox/trust/`
(S09-07-guest-trust), the terminfo database
in `/usr/local/share/terminfo`, read-only (S06-02-image-build, "Terminfo"), and the user's own home. Writes are allowed
only in the user's home, the session worktree, and temporary
directories. No profile of this layer runs simulators ("Xcode build
profile" and "Known gaps" below). The profile is generated from policy on the host and
installed by `wb-guestd`. The repository never supplies it.

### Host-guest socket

The profile denies `AF_VSOCK` sockets with
`(deny system-socket (socket-domain AF_VSOCK))`, so a project user's
processes can't open, bind, or listen on vsock and pose as
`wb-guestd` (X27-vsock-confinement). `socket()` then fails with
`EPERM`. The network rules alone aren't enough: `(deny network*)`
stops `bind` and `connect` on vsock but not `socket()`, and the
agent-safehouse modules allow every `system-socket`. A second control
backs it: `wb-guestd`'s port is below 1024, which the guest kernel
lets only root bind, and that also holds for project processes
outside the profile (S04-architecture, and "Known gaps" below). The
host doesn't rely on either to keep the guest from dialing in: it
doesn't register a vsock listener, and it opens every
connection itself (S04-architecture, B29-vsock-handoff). A project
user's requests to the host go to `wb-guestd` over a local Unix
socket, where the peer user ID names the project user. Attribution
fails closed (SEC08-proj-isolation, SEC10-audit): `wb-guestd` refuses
a peer user ID that isn't an active, unlocked project user of this
VM, such as root, a system account, a user ID created at runtime, or
a locked project user, and logs the refusal with the rule. `wb-hostd`
refuses a request for a project that isn't in the VM's effective
policy.

### Source

The agent-safehouse profiles (Apache-2.0, v0.12.0,
commit `6bded066`) are the starting point (X24-openshell-artifacts).
They are Seatbelt profile modules that start from `(deny default)`,
with placeholders for the home and work directories. Upstream
joins them with a shell script and applies them with `sandbox-exec`.
Wraith Box copies the modules it uses at the pinned commit, keeps
their license text, and generates the profile on the host. Their
network module allows every outbound IP connection, so this layer
doesn't narrow egress, and the host enforces it (S07-egress-gateway).
X24-openshell-artifacts read the profiles but didn't run them in a
guest. X27-vsock-confinement ran the modules that upstream's renderer
selects by default in a guest, for the vsock calls only.

### Xcode build profile

(X06-guest-xcode). Builds, SwiftPM and
signing get these exceptions and no others: reads of
`/Library/Preferences/com.apple.dt.Xcode.plist`,
`/Library/Developer` and `/Library/Keychains`, and lookups of
`com.apple.SecurityServer` and `com.apple.security.syspolicy`. The
generator emits exactly these exceptions. Its tests run every Xcode
build task under the profile, a build for a simulator destination
included. A build task that fails under it gets its missing rule
through a spec change that names the rule. Under the base and build
profiles `simctl` can't reach CoreSimulatorService, so simulators
and iOS tests don't run in a confined session.

### Known gaps

Processes started through LaunchServices and `launchd`
services run outside the profile. They stay inside the guest and the
project user's permissions. Simulator processes run in the user's
per-user launchd domain, outside the session's profile, and
`simctl spawn` runs a program outside it (X06-guest-xcode). They
are outside every profile of this layer. Simulator processes also
escape the resource limits `wb-guestd` sets on the session's
processes, and the VM's caps (SEC13-bounded-resources) are their
bound. launchd can start one of a project user's agents again after
the loop of Layer 1 has ended, so a process of a locked user can
still appear (X06-guest-xcode).
A process that leaves its session's POSIX session with `setsid`,
while another session of the same project runs in the VM, survives
its session's hangup and kill (Layer 1). It keeps the project
user's rights and the VM's network grants until the project's last
session ends, and it can still write to the worktree while the WIP
commit runs. `wb-guestd` logs it, and C09-guest-and-sessions
checks the log.

**Status:** Draft
