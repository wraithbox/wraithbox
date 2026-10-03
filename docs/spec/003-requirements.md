# 003 - Requirements and Threat Model

**Purpose:** Define what Wraith Box must do, how well, and what it must
defend against. Every other spec traces back to an identifier here
(`F*`, `S*`, `N*`, `C*`).

## Problem

Coding agents such as Claude Code are most useful when they can run tools
without asking permission for each step. Running them that way directly on
a developer's Mac exposes everything the developer can reach: files,
credentials, other repositories, accounts. Wraith Box runs Claude Code
inside a disposable-feeling but persistent macOS virtual machine, so the
agent keeps a native macOS toolchain while the host, its secrets and its
network stay under host-side control.

The design assumes the agent is **capable and possibly adversarial**: a
prompt injection in a README, issue, dependency or web page may take full
control of everything inside the sandbox, including root in the guest.
Agents are increasingly good at finding ways out of their environment, so
every control that matters is enforced *outside* the guest.

## Functional requirements

- **F1** `wb [args]` is a drop-in replacement for `claude [args]` in the
  current directory: same arguments, interactive TTY, piped/non-interactive
  use, and exit code.
- **F2** Works in any git repository. The first run in a repository
  registers the project automatically; no setup step per project.
- **F3** The agent never writes to the host working tree. Its work returns
  as a git branch the user reviews and merges.
- **F4** Uncommitted host changes (staged, unstaged, untracked-not-ignored)
  are carried into the session at start.
- **F5** Several sessions can run at once, in the same project and across
  projects.
- **F6** Agent tool calls run macOS-native tools (Homebrew packages, Xcode
  command line tools, optionally full Xcode).
- **F7** A project's toolchain is declared host-side (Brewfile) and
  reconciled automatically before a session starts.
- **F8** Allowlisted network destinations work for every tool without
  per-tool proxy configuration.
- **F9** A request to an unknown destination is blocked and offered to the
  user for approval out-of-band (allow once, allow for project, deny).
- **F10** A learn mode, available for trusted repositories only, records
  the destinations a project needs and proposes an allowlist.
- **F11** Dev servers listening in the guest are reachable on host
  loopback.
- **F12** Text clipboard sharing is opt-in, per direction, with approval.
- **F13** Claude Code state (conversation history, settings) persists per
  project, so `--resume`/`--continue` work within a project.
- **F14** An ephemeral mode runs a session with no persisted state.
- **F15** The user can inspect what happened: per-session audit log,
  policy explanation, diff of returned work with risky changes flagged.

## Security requirements

- **S1 Separate kernel.** The agent runs in a hardware-virtualized macOS
  guest. There is no "lighter" same-kernel mode.
- **S2 No host filesystem sharing.** No host directory is mounted into the
  guest, read-only or otherwise.
- **S3 No host code execution via returned work.** Nothing the agent
  produces executes on the host without an explicit user action; returned
  changes to files the host may later execute are flagged.
- **S4 No secrets in the guest.** This includes the model credential. The
  guest only ever holds placeholders; real credentials are injected on the
  host side of the network boundary and replace anything the guest sends.
- **S5 Default-deny egress, enforced on the host.** Only allowlisted
  hostnames are reachable. No raw-IP destinations, no host or LAN access,
  no arbitrary DNS (no DNS tunnelling), no UDP except DNS to the gateway.
- **S6 Repository-scoped writes.** Git pushes and repository-mutating API
  calls are allowed only for the project's own repositories; gists,
  repository creation and package publishing are denied by default.
- **S7 Dependency gate.** Package downloads (npm, PyPI, Go modules,
  crates) are refused when the version is younger than a minimum age or
  has a known vulnerability at or above a severity threshold.
- **S8 Per-project isolation.** Each project has its own guest user,
  state and history; one project cannot persist into another. Untrusted
  repositories can be confined to a separate VM.
- **S9 Policy lives on the host.** The guest cannot read or change policy.
  Repository-supplied configuration is ignored unless the user trusts the
  repository, and can never add credentials.
- **S10 Audit.** Every egress decision, approval and returned change is
  recorded on the host in a persisted log the guest cannot alter.
- **S11 Guest root is not a privilege.** Full control of the guest grants
  no control over enforcement, secrets or policy.
- **S12 Least privilege on the host.** No root, no extra host user
  accounts, hardened runtime, minimal entitlements; credentials are held
  by exactly one host process.
- **S13 Bounded resources.** CPU, memory and disk of each VM are capped.
- **S14 Approvals cannot be spoofed by the guest.** Approval prompts are
  never rendered in the agent's terminal stream.

## Non-functional requirements

- **N1 Startup.** Warm VM: ≤ 4 s to the Claude prompt. Suspended VM:
  ≤ 7 s. Hard ceiling 10 s (p95), excluding first-time project setup and
  the first boot after a host restart.
- **N2 Filesystem.** The workspace lives on guest-native storage. Target
  ≥ 80% of host throughput on representative workloads (`npm ci`,
  `git status` on a large repo, incremental build), measured by the
  benchmark suite (spec 011).
- **N3 Footprint.** Idle VMs suspend after a configurable period. Disk use
  grows only with divergence from the base image (copy-on-write clones).
- **N4 Host platform.** Apple Silicon, macOS 15 or later, works on
  managed (MDM) Macs.
- **N5 Platform limit.** macOS permits at most two concurrently running
  macOS guests per host; the design operates within it (spec 006).
- **N6 Operability.** Every refusal names the rule that caused it and how
  to change it.
- **N7 Maintainability.** Two implementation languages (Go, Swift),
  complexity gates, fuzzing for every parser that faces the guest.
- **N8 Licensing.** Apache-2.0; dependencies under permissive licenses
  only.

## Constraints and non-goals (v1)

- **C1** Guests are macOS only. Linux guests are a possible later spec.
- **C2** Claude Code is the supported agent. Keep agent-specific code
  behind one interface so another agent can be added later.
- **C3** Hosts other than macOS on Apple Silicon are out of scope.
- **C4** Running containers inside the sandbox is out of scope for v1.
- **C5** A full GUI app is out of scope; the UI is the CLI plus system
  notifications for approvals.

## Threat model

**Adversary.** Instructions or code running inside the guest with full
guest root: a prompt-injected or misaligned agent, a malicious dependency,
or a tool the agent runs. It can read everything in the guest, craft any
packet, and try every protocol on every interface it can see.

**Assets.** Host files; host credentials including the model credential;
the user's repositories and online accounts; other projects' code; the
integrity of the user's working tree and future host-side executions; the
user's attention (approval fatigue is an attack surface).

**Trusted.** Host kernel and Virtualization framework; the host user
account; Wraith Box host binaries; Keychain and Secure Enclave; the
operators of allowlisted services (as services, not their content).

**Boundaries.** (1) the VM; (2) the vsock RPC surface to `wb-guestd`;
(3) the virtual NIC packet surface to `wb-netd`; (4) git objects returning
to the host; (5) user approvals.

**Accepted residual risks**, documented rather than solved:

- **R1** Data sent through allowed operations to allowed destinations,
  e.g. a push to an allowed repository that is public, or prompt content
  sent to the model provider.
- **R2** Data encoded in DNS names under allowed wildcards, bounded by a
  per-session lookup budget.
- **R3** Escape from the hypervisor.
- **R4** A user approving a malicious request or merging a malicious
  change despite the flags.
- **R5** After a guest root escalation inside the shared work VM, read or
  write access to other projects' guest clones (code only; no secrets are
  present; changes still return only as reviewable branches). Untrusted
  repositories use the isolated VM to avoid this.

**Status:** Draft
