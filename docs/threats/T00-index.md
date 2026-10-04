# T00 - Threat model

**Purpose:** Who Wraith Box defends against, what it protects, what it
trusts, and which risks it accepts rather than solves.

**Adversary.** Instructions or code running inside the guest with full
guest root: a prompt-injected or misaligned agent, a malicious dependency,
or a tool the agent runs. It can read everything in the guest, craft any
packet, and try every protocol on every interface it can see.

**Assets.** Host files; host credentials including the model credential;
the user's repositories and online accounts; other projects' code; the
integrity of the user's working tree and future host-side executions; the
user's attention (approval fatigue is an attack surface).

**Trusted.** Host kernel and the platform's hypervisor and virtualization
API (S12-platforms); the host user account, including the user's WSL
distributions on Windows; Wraith Box host binaries; the platform's
credential store and hardware key store (Keychain and Secure Enclave on
macOS); the operators of allowlisted services (as services, not their
content).

**Boundaries.** (1) the VM; (2) the host-guest socket RPC surface to
`wb-guestd` (vsock or the platform equivalent); (3) the virtual NIC
packet surface to `wb-netd`; (4) git objects returning to the host;
(5) user approvals.

**Accepted residual risks**, documented rather than solved:

- **T01-allowed-channels:** Data sent through allowed operations to allowed destinations,
  for example a push to an allowed repository that is public, or prompt
  content sent to the model provider.
- **T02-dns-names:** Data encoded in DNS names under allowed wildcards, bounded by a
  per-session lookup budget.
- **T03-hypervisor-escape:** Escape from the hypervisor.
- **T04-bad-approvals:** A user approving a malicious request or merging a malicious
  change despite the flags.
- **T05-cross-proj-clones:** After a guest root escalation inside the shared work VM, read or
  write access to other projects' guest clones (code only; no secrets are
  present; changes still return only as reviewable branches). Untrusted
  repositories use the isolated VM to avoid this.
- **T06-forged-labels:** After a guest root escalation, forged program identities in the
  guest confinement layer (S13-guest-confinement). Rules narrowed to named
  programs then act as the union of the project's grants. The host
  floor (SEC05-default-deny, SEC06-repo-writes) still holds.

**Status:** Draft
