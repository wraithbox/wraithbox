# FR00 - Functional requirements

**Purpose:** What Wraith Box must do for the user. The problem these
answer is in S03-requirements.

- **FR01-drop-in:** `wb` is the single entry point for everything, structured as
  `wb [wb flags] <command> [command args]`. `wb claude [args]` is a
  drop-in replacement for `claude [args]` in the current directory: every
  argument after `claude` reaches Claude Code unchanged, with interactive
  TTY, piped/non-interactive use, and exit code preserved.
- **FR02-any-repo:** Works in any git repository. The first run in a repository
  registers the project automatically. No setup step is needed per
  project.
- **FR03-work-as-branch:** The agent never writes to the host working tree. Its work returns
  as a git branch the user reviews and merges.
- **FR04-carry-in:** Uncommitted host changes (staged, unstaged, untracked-not-ignored)
  are carried into the session at start.
- **FR05-parallel-sessions:** Several sessions can run at once, in the same project and across
  projects.
- **FR06-native-tools:** Agent tool calls run tools native to the guest operating system
  (on macOS guests: Homebrew packages, Xcode command line tools,
  optionally full Xcode).
- **FR07-toolchain-manifest:** A project's toolchain is declared host-side (on macOS guests, a
  Brewfile) and reconciled automatically before a session starts.
- **FR08-no-proxy-config:** Allowlisted network destinations work for every tool without
  per-tool proxy configuration.
- **FR09-approve-unknown:** A request to an unknown destination is blocked and offered to the
  user for approval out-of-band (allow once, allow for project, deny).
- **FR10-learn-mode:** A learn mode, available for trusted repositories only, records
  the destinations a project needs and proposes an allowlist.
- **FR11-port-forward:** Dev servers listening in the guest are reachable on host
  loopback.
- **FR12-clipboard:** Text clipboard sharing is opt-in, per direction, with approval.
- **FR13-claude-state:** Claude Code state (conversation history, settings) persists per
  project, so `--resume`/`--continue` work within a project.
- **FR14-ephemeral:** An ephemeral mode runs a session with no persisted state.
- **FR15-inspect:** The user can inspect what happened: per-session audit log,
  policy explanation, diff of returned work with risky changes flagged.
- **FR16-wsl:** On a Windows host, `wb` run inside a WSL 2 distribution works on
  repositories in that distribution, using the VMs and services of the
  Windows host. It does not start a second set of VMs inside WSL.
- **FR17-same-everywhere:** The same `wb` commands, flags, policy files, and behavior on
  every host OS. Differences are limited to what S12-platforms lists.

**Status:** Draft
