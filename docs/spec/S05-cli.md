# S05 - Command Line Interface

**Purpose:** Specify `wb`, the single user-facing command.

**Requirements:** FR01-drop-in to FR05-parallel-sessions, FR09-approve-unknown, FR13-claude-state to FR17-same-everywhere, SEC14-no-fake-approvals, NFR01-startup, NFR06-explained-refusals.

## Shape

```
wb [wb flags] <command> [command args]
```

`wb` is a dispatcher in the style of `gh` or `git`: every capability is a
subcommand of the one binary. There is no second CLI.

- **wb flags** come before the command and are the only arguments `wb`
  itself parses. Unknown flags there are an error, never passed on.
- **The command** is the first non-flag argument.
- **Command arguments** belong to the command. For agent commands they
  are not parsed by `wb` at all (below).
- `wb` with no command, `wb help`, and `wb --help` print the command list.
  `wb version` and `wb --version` print the version.
- Usage errors exit with status 2 and print the help.

## Agent commands: `wb claude`

```
wb [wb flags] claude [claude args]
```

- Every argument after `claude` reaches Claude Code unchanged, including
  ones that look like `wb` flags: `wb claude --help` shows Claude Code's
  help, `wb claude --isolated` passes `--isolated` to Claude Code. So
  `wb claude --resume`, `wb claude -p "…"`, and `wb claude mcp list`
  behave as `claude …` does, and no current or future Claude Code flag or
  subcommand can collide with Wraith Box (FR01-drop-in).
- Interactive use: raw-mode TTY relay, window-size changes forwarded,
  exit code of `claude` returned. Non-interactive use (pipes, `-p`):
  stdin/stdout streamed, stderr on its own channel.
- Users who want the bare word can alias it in their shell
  (`alias claude='wb claude'`). Wraith Box does not install such an alias.
- Further agents are further agent commands (`wb <agent> …`) behind the
  same agent interface (V1-02-claude-only). `claude` is the only one in v1.

### Session flags

Valid only before an agent command; given before any other command they
are a usage error:

| Flag | Meaning |
|---|---|
| `--isolated` | Run in the isolated VM (SEC08-proj-isolation) |
| `--ephemeral` | No persisted state for this session (FR14-ephemeral) |
| `--learn` | Learn mode; refused unless the project is trusted (FR10-learn-mode) |
| `--guest <os>` | Guest OS for this project, where the host offers more than one (S12-platforms); otherwise the project's configured or default guest |

### Global flags

| Flag | Meaning |
|---|---|
| `-C <dir>` | Act as if `wb` was started in `<dir>` |
| `--help`, `-h` | Help |
| `--version` | Version |

### Session behavior

- Must be run inside a git repository; otherwise `wb` exits with a
  message explaining why (the return path is git, S08-workspace-and-git).
- First run in a repository registers the project, creates its guest
  user and clone, then starts the session. Later runs reuse them.
- On exit, `wb` prints a short summary of the session's returned work
  and the commands to review and land it, for example:

  ```
  wraith box: session myrepo-20261003-1412-k3f9 returned 3 commits (+ WIP)
    review: wb diff myrepo-20261003-1412-k3f9
    land:   wb land myrepo-20261003-1412-k3f9
    ⚠ flagged: package.json scripts changed, .github/workflows/ci.yml changed
  ```

- During an agent session, `wb` never prints approval prompts. That
  terminal shows the agent's output, which the guest controls, so any
  prompt there could be forged (SEC14-no-fake-approvals). Approvals arrive as native
  notifications, or are answered with `wb approve` from another terminal.

## Management commands

| Command | Purpose |
|---|---|
| `wb status` | VMs, their state, running sessions, pending approvals |
| `wb sessions [--project P]` | List sessions and their returned work |
| `wb diff <session>` | Review returned work with flagged paths highlighted |
| `wb land <session> [--branch NAME]` | Fetch returned work into the host repo as a branch; never checks out or merges |
| `wb discard <session>` | Drop returned work and the session's guest worktree |
| `wb approve [<id>]` / `wb deny [<id>]` | Answer pending approvals (notification alternative) |
| `wb allow <host> [--project P]` | Add an allowlist entry (SEC05-default-deny) |
| `wb policy show/explain/edit [--project P]` | Effective policy and why a request was allowed or refused (NFR06-explained-refusals) |
| `wb learn report [--project P]` | Suggested allowlist from a learn-mode session (FR10-learn-mode) |
| `wb trust <repo>` / `wb untrust <repo>` | Allow repository-supplied configuration (SEC09-host-policy) |
| `wb cred set/list/rm <binding>` | Manage credentials held by `wb-proxyd` (S09-policy-credentials-audit) |
| `wb audit tail/search` | Read the audit log (SEC10-audit) |
| `wb vm start/stop/suspend/status` | Explicit VM control |
| `wb image build/list/use` | Base image management (S06-vm-lifecycle) |
| `wb shell [--project P]` | Debug shell as the project user, labeled as a debug shell |
| `wb setup` | Check host prerequisites (S12-platforms), install and start the per-user services |
| `wb help`, `wb version` | Help and version |

Each command has its own flags after the command name. Names of
commands are reserved: a new agent command may not reuse one.

`wb approve` and `wb deny` print what is being approved. That text
includes guest-influenced values (hostnames, paths); `wb` strips control
and escape sequences from every such value before printing it, so the
guest cannot rewrite the approval screen (SEC14-no-fake-approvals).

## On WSL

Inside a WSL 2 distribution on a Windows host, `wb` runs in client mode
(FR16-wsl, S12-platforms): the same commands and flags, served by the Windows
host's `wb-hostd`. The repository, the terminal, and `wb land` are on the
WSL side; VMs, policy, credentials, and audit are on the Windows side.

## Naming

- Project id: stable hash of the repository's location (including the
  host realm: native host, or WSL distribution name) plus the URL of its
  first remote. A human-readable name is derived from the directory.
- Session id: `<project-name>-<yyyymmdd>-<hhmm>-<4 random chars>`.
- Returned work lands on host branches `wb/<session-id>` by default.

**Status:** Draft
