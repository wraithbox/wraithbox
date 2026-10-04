# 003 - Requirements and Threat Model

**Purpose:** Define what Wraith Box must do, how well, and what it must
defend against. Every other spec traces back to an identifier here
(`F*`, `S*`, `N*`, `C*`, `R*`).

Each identifier is a number and a short slug, as in `S6-repo-writes`.
Both stay fixed once published. Write the full form, so a reader does
not need this page open. The number alone (`S6-repo-writes`) is accepted as an
alias, and the docs site expands it.

## Problem

Coding agents such as Claude Code are most useful when they can run tools
without asking permission for each step. Running them that way directly on
a developer's machine exposes everything the developer can reach: files,
credentials, other repositories, accounts. Wraith Box runs Claude Code
inside a disposable-feeling but persistent virtual machine, so the agent
keeps a native toolchain for the operating system it works on while the
host, its secrets, and its network stay under host-side control.

The first delivery target is macOS guests on macOS hosts. The design is
for macOS, Windows, and Linux hosts, with Linux and Windows guests on all
of them (spec 012); platform-specific code is kept behind narrow
interfaces so later platforms do not reshape the core.

The design assumes the agent is **capable and possibly adversarial**: a
prompt injection in a README, issue, dependency, or web page may take full
control of everything inside the sandbox, including root in the guest.
Agents are increasingly good at finding ways out of their environment, so
every control that matters is enforced *outside* the guest.

## Functional requirements

- **F1-drop-in:** `wb` is the single entry point for everything, structured as
  `wb [wb flags] <command> [command args]`. `wb claude [args]` is a
  drop-in replacement for `claude [args]` in the current directory: every
  argument after `claude` reaches Claude Code unchanged, with interactive
  TTY, piped/non-interactive use, and exit code preserved.
- **F2-any-repo:** Works in any git repository. The first run in a repository
  registers the project automatically. No setup step is needed per
  project.
- **F3-work-as-branch:** The agent never writes to the host working tree. Its work returns
  as a git branch the user reviews and merges.
- **F4-carry-in:** Uncommitted host changes (staged, unstaged, untracked-not-ignored)
  are carried into the session at start.
- **F5-parallel-sessions:** Several sessions can run at once, in the same project and across
  projects.
- **F6-native-tools:** Agent tool calls run tools native to the guest operating system
  (on macOS guests: Homebrew packages, Xcode command line tools,
  optionally full Xcode).
- **F7-toolchain-manifest:** A project's toolchain is declared host-side (on macOS guests, a
  Brewfile) and reconciled automatically before a session starts.
- **F8-no-proxy-config:** Allowlisted network destinations work for every tool without
  per-tool proxy configuration.
- **F9-approve-unknown:** A request to an unknown destination is blocked and offered to the
  user for approval out-of-band (allow once, allow for project, deny).
- **F10-learn-mode:** A learn mode, available for trusted repositories only, records
  the destinations a project needs and proposes an allowlist.
- **F11-port-forward:** Dev servers listening in the guest are reachable on host
  loopback.
- **F12-clipboard:** Text clipboard sharing is opt-in, per direction, with approval.
- **F13-claude-state:** Claude Code state (conversation history, settings) persists per
  project, so `--resume`/`--continue` work within a project.
- **F14-ephemeral:** An ephemeral mode runs a session with no persisted state.
- **F15-inspect:** The user can inspect what happened: per-session audit log,
  policy explanation, diff of returned work with risky changes flagged.
- **F16-wsl:** On a Windows host, `wb` run inside a WSL 2 distribution works on
  repositories in that distribution, using the VMs and services of the
  Windows host. It does not start a second set of VMs inside WSL.
- **F17-same-everywhere:** The same `wb` commands, flags, policy files, and behavior on
  every host OS. Differences are limited to what spec 012 lists.

## Security requirements

- **S1-separate-kernel: Separate kernel.** The agent runs in a hardware-virtualized guest
  with its own kernel, never sharing a kernel with the host or with
  another environment of the user (for example a WSL distribution). There
  is no "lighter" same-kernel mode.
- **S2-no-host-fs-share: No host filesystem sharing.** No host directory is mounted into the
  guest, read-only or otherwise.
- **S3-no-host-exec: No host code execution via returned work.** Nothing the agent
  produces executes on the host without an explicit user action; returned
  changes to files the host may later execute are flagged.
- **S4-no-guest-secrets: No secrets in the guest.** This includes the model credential. The
  guest only ever holds placeholders; real credentials are injected on the
  host side of the network boundary and replace anything the guest sends.
- **S5-default-deny: Default-deny egress, enforced on the host.** Only allowlisted
  hostnames are reachable. No raw-IP destinations, no host or LAN access,
  no arbitrary DNS (no DNS tunneling), no UDP except DNS to the gateway.
- **S6-repo-writes: Repository-scoped writes.** Git pushes and repository-mutating API
  calls are allowed only for the project's own repositories; gists,
  repository creation, and package publishing are denied by default.
- **S7-dep-gate: Dependency gate.** Package downloads (npm, PyPI, Go modules,
  crates) are refused when the version is younger than a minimum age or
  has a known vulnerability at or above a severity threshold.
- **S8-proj-isolation: Per-project isolation.** Each project has its own guest user,
  state and history; one project cannot persist into another. Untrusted
  repositories can be confined to a separate VM.
- **S9-host-policy: Policy is held on the host.** The guest cannot read or change policy.
  Repository-supplied configuration is ignored unless the user trusts the
  repository, and can never add credentials.
- **S10-audit: Audit.** Every egress decision, approval, and returned change is
  recorded on the host in a persisted log the guest cannot alter.
- **S11-root-gains-nothing: Guest root is not a privilege.** Full control of the guest grants
  no control over enforcement, secrets, or policy.
- **S12-least-privilege: Least privilege on the host.** No root or administrator rights
  at run time and no extra host user accounts. The platform's hardening
  applies (hardened runtime and minimal entitlements on macOS, the
  equivalents in spec 012 elsewhere). Credentials are held by exactly one
  host process.
  One-time OS prerequisites (enabling a virtualization feature, access to
  the hypervisor device) are documented and checked by `wb setup`, never
  performed silently.
- **S13-bounded-resources: Bounded resources.** CPU, memory and disk of each VM are capped.
- **S14-no-fake-approvals: Approvals cannot be spoofed by the guest.** Approval prompts are
  never rendered in the agent's terminal stream.

## Non-functional requirements

- **N1-startup: Startup.** Warm VM: ≤ 4 s to the Claude prompt. Suspended VM:
  ≤ 7 s. Hard ceiling 10 s (p95), excluding first-time project setup and
  the first boot after a host restart.
- **N2-fs-speed: Filesystem.** The workspace is on guest-native storage. Target
  ≥ 80% of host throughput on representative workloads (`npm ci`,
  `git status` on a large repo, incremental build), measured by the
  benchmark suite (spec 011).
- **N3-footprint: Footprint.** Idle VMs suspend after a configurable period. Disk use
  grows only with divergence from the base image (copy-on-write clones).
- **N4-host-platforms: Host platforms.** First target: Apple Silicon, macOS 15 or later.
  Later: Windows 11 (Home and Pro) and Ubuntu LTS, with other Linux
  distributions considered after that. Works on managed (MDM or
  domain-joined) machines on every host OS.
- **N5-two-macos-vms: Platform limit.** macOS permits at most two concurrently running
  macOS guests per host. The design works within that limit (spec 006).
  Other guest operating systems are limited only by resources.
- **N6-explained-refusals: Operability.** Every refusal names the rule that caused it and how
  to change it.
- **N7-maintainability: Maintainability.** Most code is cross-platform Go. Platform-native
  code (Swift on macOS, and the native toolchain of Windows or Linux only
  where Go cannot do the job) is confined to small components behind
  defined contracts (spec 010). Complexity gates, fuzzing for every parser
  that faces the guest, Go CI on all three host OSes from the start.
- **N8-licensing: Licensing.** Apache-2.0; dependencies under permissive licenses
  only.

## Constraints and non-goals (v1)

- **C1-macos-first:** The first delivery target is macOS guests on macOS hosts.
  Linux guests (Ubuntu LTS first) and Windows 11 guests follow, on every
  host that can run them (spec 012). macOS guests run only on macOS hosts.
- **C2-claude-only:** Claude Code is the supported agent. Keep agent-specific code
  behind one interface so another agent can be added later.
- **C3-win-linux-later:** Windows and Linux hosts are designed for but not delivered in
  v1. Decisions that would make them harder need a spec change.
- **C4-no-containers:** Running containers inside the sandbox is out of scope for v1.
- **C5-cli-only:** A full GUI app is out of scope; the UI is the CLI plus system
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

**Trusted.** Host kernel and the platform's hypervisor and virtualization
API (spec 012); the host user account, including the user's WSL
distributions on Windows; Wraith Box host binaries; the platform's
credential store and hardware key store (Keychain and Secure Enclave on
macOS); the operators of allowlisted services (as services, not their
content).

**Boundaries.** (1) the VM; (2) the host-guest socket RPC surface to
`wb-guestd` (vsock or the platform equivalent); (3) the virtual NIC
packet surface to `wb-netd`; (4) git objects returning to the host;
(5) user approvals.

**Accepted residual risks**, documented rather than solved:

- **R1-allowed-channels:** Data sent through allowed operations to allowed destinations,
  for example a push to an allowed repository that is public, or prompt
  content sent to the model provider.
- **R2-dns-names:** Data encoded in DNS names under allowed wildcards, bounded by a
  per-session lookup budget.
- **R3-hypervisor-escape:** Escape from the hypervisor.
- **R4-bad-approvals:** A user approving a malicious request or merging a malicious
  change despite the flags.
- **R5-cross-proj-clones:** After a guest root escalation inside the shared work VM, read or
  write access to other projects' guest clones (code only; no secrets are
  present; changes still return only as reviewable branches). Untrusted
  repositories use the isolated VM to avoid this.
- **R6-forged-labels:** After a guest root escalation, forged program identities in the
  guest confinement layer (spec 013). Rules narrowed to named
  programs then act as the union of the project's grants. The host
  floor (S5-default-deny, S6-repo-writes) still holds.

**Status:** Draft
