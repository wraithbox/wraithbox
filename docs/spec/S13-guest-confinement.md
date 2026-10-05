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
"Enforced per VM"). Every project with a session in a VM can use the
network grants and credential bindings of the others in it, without
guest root (T11-shared-vm-grants). Only a label from this layer
(Layer 3) can tell projects apart on the network, and only against a
process without guest root.

## Layer 1: process hardening (v1)

Applied by `wb-guestd` to every process it starts for a project user:

- a project user per project (S06-vm-lifecycle), never root. Project
  users keep files apart, not network traffic (T11-shared-vm-grants);
- `RLIMIT_CORE` set to 0, and an environment built from an allowlist
  (S09-policy-credentials-audit);
- when the host-guest socket drops, `wb-guestd` stops the session's
  process group, so no session runs on without host session control. It
  resumes the group when the link returns.

## Layer 2: Seatbelt filesystem profile (v1)

Each session's process tree runs under a Seatbelt profile
(`sandbox_init`). The kernel enforces it, children inherit it, and a
process cannot remove it, much like Landlock.

- **Profile.** Reads are denied by default, with an allowlist for the
  system, Homebrew, Xcode, and the user's own home. Writes are allowed
  only in the user's home, the session worktree, and temporary
  directories. The profile is generated from policy on the host and
  installed by `wb-guestd`. The repository never supplies it.
- **Host-guest socket.** The profile denies `AF_VSOCK` sockets, so a
  project user's processes can't dial the host directly. The host
  doesn't rely on it: it refuses or binds guest-initiated connections
  itself (S04-architecture, X18-vsock-handoff).
- **Source.** The agent-safehouse profiles (Apache-2.0, v0.12.0,
  commit `6bded066`) are the starting point (X24-openshell-artifacts).
  They are Seatbelt profile modules that start from `(deny default)`,
  with placeholders for the home and work directories. Upstream
  joins them with a shell script and applies them with `sandbox-exec`.
  Wraith Box copies the modules it uses at the pinned commit, keeps
  their license text, and generates the profile on the host. Their
  network module allows every outbound connection, so this layer
  doesn't narrow egress, and the host enforces it (S07-egress-gateway).
  X24-openshell-artifacts read the profiles but didn't run them in a
  guest.
- **Known gaps.** Processes started through LaunchServices and `launchd`
  services run outside the profile. They stay inside the guest and the
  project user's permissions. Xcode and simulators need exceptions
  (spike X06-guest-xcode).

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
