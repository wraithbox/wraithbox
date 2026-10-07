# S13 - Guest Confinement

**Purpose:** Controls inside the guest that narrow what a project user's
processes can do, and tell the host which program opened each
connection. They add a layer on top of the host-side controls and never
replace them.

**Requirements:** SEC05-default-deny, SEC06-repo-writes, SEC08-proj-isolation, SEC11-root-gains-nothing, FR15-inspect, NFR06-explained-refusals. Residual risks T06-forged-labels and T11-shared-vm-grants.

This spec describes macOS guests, the v1 target. OpenShell's Linux
sandbox runtime (Landlock, seccomp, process identity from `/proc`) is
the model for this layer. Linux and Windows guests get their own
mechanisms in S12-platforms when they arrive.

## Principle: narrow, never widen

The guest kernel, or software that guest root can reach, enforces each
control here. T00-index assumes the attacker may hold guest root
(SEC11-root-gains-nothing). So:

- **The host floor is complete.** The host meets each `SEC*` control
  (S07-egress-gateway to S09-policy-credentials-audit) with this layer absent, disabled, or compromised.
- **Guest controls can only deny.** A rule that uses a program identity
  narrows an allow rule that the host would grant anyway. A missing or
  malformed identity matches no narrowed rule, so narrowed rules fail
  closed.
- **Identities are labels.** They are recorded in the audit log as
  reported by the guest (S09-policy-credentials-audit), never as proof.

Guest root can forge identities, which collapses per-program and
per-project rules into the union of the VM's grants. That is residual
risk T06-forged-labels.

The host floor is per VM, not per project (S07-egress-gateway,
"Enforced per VM"). Any process in a VM can use the network grants and
credential bindings of every project with a session in it, without
guest root (T11-shared-vm-grants). Only a label from this layer
(Layer 3) can tell projects apart on the network, and only against a
process without guest root.

## Layer 1: process hardening (v1)

Applied by `wb-guestd` to every process it starts for a project user:

- a project user per project (S06-vm-lifecycle), never root. Project
  users keep files apart, not network traffic (T11-shared-vm-grants);
- `RLIMIT_CORE` set to 0, and an environment built from an allowlist
  (S09-policy-credentials-audit);
- every operation under a project user's home, the worktree, the
  carry-in and the WIP commit and push included, runs in a child
  process as that user with the session's environment allowlist.
  `wb-guestd`, running as root, never opens a path there
  (S06-vm-lifecycle, "Session lifecycle");
- when the host-guest socket drops, `wb-guestd` stops every process of
  every project user in the VM, in a loop until none is left running,
  so no session runs on without host session control. It records
  which processes were already stopped, such as a `claude` stopped
  with job control. When the link returns, it resumes a session only
  when `wb-hostd` answers "resume", and then sends `SIGCONT` only to
  the processes it stopped itself. On "finish" or "end" it hangs up or
  kills the session (S06-vm-lifecycle, "Session lifecycle"). A
  project user's processes that it stopped and that no resumed
  session holds stay stopped until that user's processes are killed.
  A new `wb-guestd` process never takes over the sessions of an
  earlier one. A VM with a session is never saved for idleness. A
  restore then never has a session to resume;
- when a session's client goes (its terminal closed, or `wb` was
  killed), `wb-guestd` hangs up `claude`. When it hasn't exited 10
  seconds later, `wb-guestd` sends `SIGKILL` to every process in the
  session's POSIX session (S06-vm-lifecycle, "Session helper"), and
  only to those whose user ID is the session's project user. When
  the project user has no other session in the VM, a lost one
  included, it then kills every
  process of that user, in a loop until none remain. Otherwise it logs
  each process of that user that is in no session's POSIX session,
  with the rule, and leaves it (Known gaps below);
- when `wb-hostd` ends a project's last session in the VM, debug
  shells included, `wb-guestd` locks that project user, so nothing new
  starts as it, and kills its processes in a loop until none remain.
  A lost session counts here until recovery, `wb discard`,
  `wb project rm` or its deadline ends it (S06-vm-lifecycle,
  "Lost"), so recovery can still commit and push as that user. Until
  then the user's remaining processes stay stopped: `wb-guestd` stops
  them at the link drop, and a new `wb-guestd` process stops every
  project user's processes when it starts, before it serves the host.
  On its first connection `wb-hostd` names the project users that have
  a lost session in the VM, and `wb-guestd` kills the stopped
  processes of every other project user, in a loop until none remain.
  A stopped VM leaves none.
  This holds against a process without guest root only. None is meant
  to keep running after its project leaves the VM's
  effective policy or during another project's learn-mode session (S07-egress-gateway, "Approvals and
  learning").

## Layer 2: Seatbelt filesystem profile (v1)

Each session's process tree runs under a Seatbelt profile
(`sandbox_init`). The kernel enforces it, children inherit it, and a
process cannot remove it, much like Landlock.

- **Profile.** Reads are denied by default, with an allowlist for the
  system, Homebrew, Xcode, and the user's own home. Writes are allowed
  only in the user's home, the session worktree, and temporary
  directories. The profile is generated from policy on the host and
  installed by `wb-guestd`. The repository never supplies it.
- **Host-guest socket.** The profile denies `AF_VSOCK` sockets with
  `(deny system-socket (socket-domain AF_VSOCK))`, so a project user's
  processes can't open, bind, or listen on vsock and pose as
  `wb-guestd` (X27-vsock-confinement). `socket()` then fails with
  `EPERM`. The network rules alone aren't enough: `(deny network*)`
  stops `bind` and `connect` on vsock but not `socket()`, and the
  agent-safehouse modules allow every `system-socket`. A second control
  backs it: `wb-guestd`'s port is below 1024, which the guest kernel
  lets only root bind, and that also holds for project processes
  outside the profile (S04-architecture, "Known gaps" below). The
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
- **Source.** The agent-safehouse profiles (Apache-2.0, v0.12.0,
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
- **Known gaps.** Processes started through LaunchServices and `launchd`
  services run outside the profile. They stay inside the guest and the
  project user's permissions. Xcode and simulators need exceptions
  (spike X06-guest-xcode).
  A process that leaves its session's POSIX session with `setsid`,
  while another session of the same project runs in the VM, survives
  its session's hangup and kill (Layer 1). It keeps the project
  user's rights and the VM's network grants until the project's last
  session ends, and it can still write to the worktree while the WIP
  commit runs. `wb-guestd` logs it, and S11-verification-and-spikes
  checks the log.

## Layer 3: per-program network attribution (after X14-flow-attribution)

A Network Extension system extension in the guest
(`NETransparentProxyProvider`) sees every TCP and UDP flow together
with the source process's audit token. For each flow it reports a
label to the host:

- executable path, signing identifier, team identifier, and code
  directory hash (cdhash). Apple Silicon runs only signed binaries, at
  least ad hoc signed. The cdhash identifies the file the way OpenShell's
  SHA-256 does.
- the user ID of the process from the same audit token, which names
  the project user and so the project. Parallel sessions of one
  project run as the same user. The label names a session only while
  its project has one session running.

`wb-netd` attaches the label to the stream it hands to `wb-proxyd`.
Rules then match on it (S09-policy-credentials-audit):

- **Matching.** A rule's `binaries` entry matches the program that
  opened the connection. Ancestry is not available from a Network
  Extension, so helpers must be listed themselves (for example `git` and
  `git-remote-https`).
- **Pinning.** The first cdhash seen for a path is recorded. A later
  connection from the same path with another cdhash is denied and
  audited, as in OpenShell.
- **Flows the extension does not see** (system daemons, a killed or
  unloaded extension) arrive unlabeled and only match rules with no
  program narrowing.

How labels reach the host (a header on each flow, or a side channel
over the host-guest socket keyed by the connection's addresses and
ports) is decided by spike X14-flow-attribution. Either way the host parses guest bytes,
and the parser gets a fuzz target (S11-verification-and-spikes).

**Apple prerequisites.** A Developer ID with the Network Extension
system extension entitlement. The extension is approved once in the
base image at build time (S06-vm-lifecycle). Production guests keep System
Integrity Protection on. That is expected to stop guest root from
replacing the extension, but not from killing it. X14-flow-attribution checks both.
Development guests may turn it off.

## Layer 4: Endpoint Security (optional, after X15-endpoint-security)

An Endpoint Security client in the guest adds:

- ancestry, so a rule for a program also covers the programs it starts,
  matching OpenShell's semantics;
- an exec allowlist per project, by cdhash or signing identity;
- file-open authorization for cases Seatbelt profiles cannot express.

Its entitlement is granted by Apple on request, which takes time and
may be refused. Without the entitlement, Wraith Box ships without this
layer. Spike X15-endpoint-security tracks it.

**Status:** Draft
