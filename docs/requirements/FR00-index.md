# FR00 - Functional requirements

**Purpose:** What Wraith Box must do for the user. The problem these
answer is in S03-requirements.

| ID | Description | Status |
|----|-------------|--------|
| FR01-drop-in | Drop-in for claude | V1-M4-claude-end-to-end |
| FR02-any-repo | Any git repository | V1-M2-walking-skeleton, V1-M4-claude-end-to-end |
| FR03-work-as-branch | Work returns as a branch | V1-M4-claude-end-to-end |
| FR04-carry-in | Uncommitted changes carried in | V1-M4-claude-end-to-end |
| FR05-parallel-sessions | Parallel sessions | V1-M4-claude-end-to-end |
| FR06-native-tools | Native guest tools | No milestone |
| FR07-toolchain-manifest | Declared toolchain | No milestone |
| FR08-no-proxy-config | No per-tool proxy settings | V1-M3-network-floor |
| FR09-approve-unknown | Approve unknown destinations | V1-M5-approvals |
| FR10-learn-mode | Learn mode | V1-M5-approvals |
| FR11-port-forward | Port forwarding | No milestone |
| FR12-clipboard | Opt-in clipboard | No milestone |
| FR13-claude-state | Claude Code state per project | V1-M4-claude-end-to-end |
| FR14-ephemeral | Ephemeral sessions | No milestone |
| FR15-inspect | Inspect what happened | V1-M5-approvals |
| FR16-wsl | WSL on Windows | No milestone |
| FR17-same-everywhere | Same on every host | No milestone |

## Requirements

- **FR01-drop-in: Drop-in for claude.** `wb` is the single entry point for everything, structured as
  `wb [wb flags] <command> [command args]`. `wb claude [args]` is a
  drop-in replacement for `claude [args]` in the current directory: every
  argument after `claude` reaches Claude Code unchanged, with interactive
  TTY, piped/non-interactive use, and exit code preserved.
- **FR02-any-repo: Any git repository.** Works in any git repository. The first run in a repository
  registers the project automatically. No setup step is needed per
  project.
- **FR03-work-as-branch: Work returns as a branch.** The agent never writes to the host working tree. Its work returns
  as a git branch the user reviews and merges.
- **FR04-carry-in: Uncommitted changes carried in.** Uncommitted host changes (staged, unstaged, untracked-not-ignored)
  are carried into the session at start.
- **FR05-parallel-sessions: Parallel sessions.** Several sessions can run at once, in the same project and across
  projects.
- **FR06-native-tools: Native guest tools.** Agent tool calls run tools native to the guest operating system
  (on macOS guests: Homebrew packages, Xcode command line tools,
  optionally full Xcode).
- **FR07-toolchain-manifest: Declared toolchain.** A project's toolchain is declared host-side (on macOS guests, a
  Brewfile) and reconciled automatically before a session starts.
- **FR08-no-proxy-config: No per-tool proxy settings.** Allowlisted network destinations work for every tool without
  per-tool proxy configuration.
- **FR09-approve-unknown: Approve unknown destinations.** A request to an unknown destination is blocked and offered to the
  user for approval out-of-band (allow once, allow for project, deny).
- **FR10-learn-mode: Learn mode.** A learn mode, available for trusted repositories only, records
  the destinations a project needs and proposes an allowlist.
- **FR11-port-forward: Port forwarding.** Dev servers listening in the guest are reachable on host
  loopback.
- **FR12-clipboard: Opt-in clipboard.** Text clipboard sharing is opt-in, per direction, with approval.
- **FR13-claude-state: Claude Code state per project.** Claude Code state (conversation history, settings) persists per
  project, so `--resume`/`--continue` work within a project.
- **FR14-ephemeral: Ephemeral sessions.** An ephemeral mode runs a session with no persisted state.
- **FR15-inspect: Inspect what happened.** The user can inspect what happened: per-session audit log,
  policy explanation, diff of returned work with risky changes flagged.
  Network events are attributed to a session only by a label the guest
  reports, and the log shows them as reported by the guest
  (T11-shared-vm-grants).
- **FR16-wsl: WSL on Windows.** On a Windows host, `wb` run inside a WSL 2 distribution works on
  repositories in that distribution, using the VMs and services of the
  Windows host. It does not start a second set of VMs inside WSL.
- **FR17-same-everywhere: Same on every host.** The same `wb` commands, flags, policy files, and behavior on
  every host OS. Differences are limited to what S12-platforms lists.
