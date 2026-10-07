# SEC00 - Security requirements

**Purpose:** The security controls Wraith Box enforces. None of them is
ever weakened to make something work (`AGENTS.md`), and each holds
against the adversary of T00-index.

| ID | Description | Status |
|----|-------------|--------|
| SEC01-separate-kernel | Separate kernel | V1-M2-walking-skeleton |
| SEC02-no-host-fs-share | No host filesystem sharing | V1-M2-walking-skeleton |
| SEC03-no-host-exec | No host code execution via returned work | V1-M4-claude-end-to-end |
| SEC04-no-guest-secrets | No secrets in the guest | V1-M2-walking-skeleton, V1-M4-claude-end-to-end |
| SEC05-default-deny | Default-deny egress, enforced on the host | V1-M3-network-floor |
| SEC06-repo-writes | Repository-scoped writes | V1-M4-claude-end-to-end |
| SEC07-dep-gate | Dependency gate | V1-M5-approvals |
| SEC08-proj-isolation | Per-project isolation | V1-M2-walking-skeleton |
| SEC09-host-policy | Policy is held on the host | V1-M5-approvals |
| SEC10-audit | Audit | V1-M3-network-floor |
| SEC11-root-gains-nothing | Guest root is not a privilege | V1-M3-network-floor |
| SEC12-least-privilege | Least privilege on the host | V1-M6-release-gate |
| SEC13-bounded-resources | Bounded resources | V1-M6-release-gate |
| SEC14-no-fake-approvals | Approvals cannot be spoofed by the guest | V1-M5-approvals |

## Requirements

- **SEC01-separate-kernel: Separate kernel.** The agent runs in a hardware-virtualized guest
  with its own kernel, never sharing a kernel with the host or with
  another environment of the user (for example a WSL distribution). There
  is no "lighter" same-kernel mode.
- **SEC02-no-host-fs-share: No host filesystem sharing.** No host directory is mounted into the
  guest, read-only or otherwise.
- **SEC03-no-host-exec: No host code execution via returned work.** Nothing the agent
  produces executes on the host without an explicit user action; returned
  changes to files the host may later execute are flagged.
- **SEC04-no-guest-secrets: No secrets in the guest.** This includes the model credential. The
  guest only ever holds placeholders; real credentials are injected on the
  host side of the network boundary and replace anything the guest sends.
  A credential that opens nothing outside its own VM, such as the
  guest's own `wbadmin` password, isn't a host secret. It enters its VM
  only at first contact, before project code runs, and is rotated per
  VM (S15-least-privilege).
- **SEC05-default-deny: Default-deny egress, enforced on the host.** Only allowlisted
  hostnames are reachable. No raw-IP destinations, no host or LAN access,
  no arbitrary DNS (no DNS tunneling), no UDP except DNS to the gateway.
- **SEC06-repo-writes: Repository-scoped writes.** Git pushes and repository-mutating API
  calls are allowed only for the repositories of the projects that
  have a session in the VM. The host can't tell the projects in one VM
  apart, so it enforces this per VM, and the projects that share a VM
  share their write grants (T11-shared-vm-grants). Gists, repository
  creation, and package publishing are denied by default. Work
  returned over the host-guest socket lands only in the session's own
  branch of its project's landing repository (S08-workspace-and-git).
- **SEC07-dep-gate: Dependency gate.** Package downloads (npm, PyPI, Go modules,
  crates) are refused when the version is younger than a minimum age or
  has a known vulnerability at or above a severity threshold.
- **SEC08-proj-isolation: Per-project isolation.** Each project has its own guest user,
  state and history, and one project cannot persist into another. Per
  project: the guest user with its home and clone, enforced by the
  guest kernel (T05-cross-proj-clones), and the host-side settings,
  policy file and landing repository. Per VM: network grants,
  credential bindings and approvals of the projects with a session in
  the VM, which any process in the VM can use (T11-shared-vm-grants).
  A repository the user doesn't trust runs in the isolated VM, by
  `--isolated` or `placement = isolated`. New projects start in the
  work VM (T13-new-project-grants). A project whose grants must stay
  apart is set to `placement = isolated`, and `wb-hostd` refuses to
  start another project next to it (S06-vm-lifecycle, "VMs").
- **SEC09-host-policy: Policy is held on the host.** The guest cannot read or change policy.
  Repository-supplied configuration is ignored unless the user turns on
  the project's `config_trust` (`wb trust`), and can never add
  credentials. `config_trust` doesn't decide the VM.
- **SEC10-audit: Audit.** Every egress decision, approval, and returned change is
  recorded on the host in a persisted log the guest cannot alter.
- **SEC11-root-gains-nothing: Guest root is not a privilege.** Full control of the guest grants
  no control over enforcement, secrets, or policy.
- **SEC12-least-privilege: Least privilege on the host.** No root or administrator rights
  at run time and no extra host user accounts. The platform's hardening
  applies (hardened runtime and minimal entitlements on macOS, the
  equivalents in S12-platforms elsewhere). Credentials are nominally
  held by the configured secret store. Each host process has a declared
  set of items, each with the access it may have: write, fetch, or use
  in place. It gets them just in time, for no longer than it needs
  them. A credential never appears in arguments, the environment, a
  file, a log line, a gRPC message field, an audit record, or an error
  message (S15-least-privilege).
  One-time OS prerequisites (enabling a virtualization feature, access to
  the hypervisor device) are documented and checked by `wb setup`, never
  performed silently.
- **SEC13-bounded-resources: Bounded resources.** CPU, memory and disk of each VM are capped.
  So is the host work a VM can cause: every host handler that takes
  input from a guest limits, per VM, the rate and the size of that
  input and the work it runs at once, and refuses what is over a limit
  (S04-architecture, "Host work the guest can cause").
- **SEC14-no-fake-approvals: Approvals cannot be spoofed by the guest.** Approval prompts are
  never rendered in the agent's terminal stream.
