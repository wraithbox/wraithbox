# S13 - Guest Confinement

**Purpose:** Controls inside the guest that narrow what a project user's
processes can do, and tell the host which program opened each
connection. They add a layer on top of the host-side controls and never
replace them.

**Requirements:** SEC05-default-deny, SEC06-repo-writes, SEC08-proj-isolation, SEC11-root-gains-nothing, FR15-inspect, NFR06-explained-refusals. Residual risks T06-forged-labels and T11-shared-vm-grants.

**Tests:** C03-policy-and-approvals, C09-guest-and-sessions

This spec describes macOS guests, the v1 target. OpenShell's Linux
sandbox runtime (Landlock, seccomp, process identity from `/proc`) is
the model for this layer. Linux and Windows guests get their own
mechanisms in S12-platforms when they arrive.

Briefs: B20-guest-xcode, B29-vsock-handoff

### Pages

| Page | What it covers |
|---|---|
| S13-01-process-hardening | Layer 1: project users, limits, and stopping and killing a project user's processes |
| S13-02-filesystem-profile | Layer 2: the Seatbelt profile, the host-guest socket, the Xcode build profile and the known gaps |
| S13-03-network-attribution | Layer 3: the per-program label on each flow, after X14-flow-attribution |
| S13-04-endpoint-security | Layer 4: Endpoint Security, optional, after X15-endpoint-security |

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

The host floor is per VM, not per project (S07-01-per-vm-policy). Any process in a VM can use the network grants and
credential bindings of every project with a session in it, without
guest root (T11-shared-vm-grants). Only a label from this layer
(Layer 3) can tell projects apart on the network, and only against a
process without guest root.

**Status:** Draft
