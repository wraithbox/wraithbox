# S05-01 - Agent Commands

**Purpose:** How `wb claude` passes its arguments to Claude Code, how it runs
interactive and non-interactive sessions, and what a session does.

## Design

### `wb claude`

```
wb [wb flags] claude [claude args]
```

- Every argument after `claude` reaches Claude Code unchanged, including
  ones that look like `wb` flags: `wb claude --help` shows Claude Code's
  help, `wb claude --isolated` passes `--isolated` to Claude Code. So
  `wb claude --resume`, `wb claude -p "…"`, and `wb claude mcp list`
  behave as `claude …` does, and no current or future Claude Code flag or
  subcommand can collide with Wraith Box (FR01-drop-in).
- Interactive use, when `wb`'s standard input and standard output are
  both terminals: raw-mode TTY relay, and `claude` runs on a PTY in the
  guest. Window-size changes are forwarded. Standard error goes to the
  PTY too, unless `wb`'s standard error isn't a terminal, in which case
  it is on its own channel. Non-interactive use, when either isn't a
  terminal (a pipe, or `claude -p … > out.txt`): stdin and stdout
  streamed, stderr on its own channel. `wb` doesn't read `claude`'s
  arguments to decide, so `wb claude -p "…"` in a terminal is
  interactive use, as `claude -p "…"` there has a terminal. The exit
  status is in S05-02-exit-status-and-signals.
- Users who want the bare word can alias it in their shell
  (`alias claude='wb claude'`). Wraith Box does not install such an alias.
- Further agents are further agent commands (`wb <agent> …`) behind the
  same agent interface (V1-02-claude-only, S14-claude-in-guest, "Agent
  interface"). `claude` is the only one in v1. How Claude Code gets into
  the guest, which of the user's `~/.claude/` goes with it, and which
  Claude Code commands don't work in a guest are in S14-claude-in-guest.

### Session flags

Valid only before an agent command; given before any other command they
are a usage error:

| Flag | Meaning |
|---|---|
| `--isolated` | Run this session in the isolated VM, whatever the project's `placement` (SEC08-proj-isolation, S06-04-vms-and-placement) |
| `--ephemeral` | No persisted state for this session (FR14-ephemeral) |
| `--learn` | Learn mode; refused unless the project's `placement` is `work`, and refused with `--isolated` (FR10-learn-mode). `config_trust` doesn't count. `wb-hostd` also refuses it while a session of another project, a debug shell included, runs in the VM, and refuses other projects' sessions while it runs (S07-11-approvals-and-learning) |
| `--guest <os>` | Guest OS for this project, where the host offers more than one (S12-platforms); otherwise the project's configured or default guest |

### Session behavior

- Must be run inside a git repository; otherwise `wb` exits with 255
  and a message explaining why (the return path is git,
  S08-workspace-and-git).
- Closing the terminal ends the session (decided on I46,
  B46-session-lifecycle). `claude` gets a hangup, and its work is
  committed as WIP and pushed as on a normal exit. V1 has no detach
  and no `wb attach`. `wb claude --continue` starts a new
  session at the host's `HEAD` that resumes the project's most recent
  conversation. The files the old session left uncommitted are only in
  its WIP commit, and `wb land` brings them back. The states of a
  session are in S06-09-session-lifecycle.
- First run in a repository registers the project, creates its guest
  user and clone, then starts the session. Later runs reuse them.
  At registration `wb` creates the project directory
  `<data>/projects/<project-id>` and the `export.git` in it, because
  `wb-hostd` can't write either (S08-02-export-repository).
- On macOS, within 30 days of the expiry date of the signing profile in
  the helper bundles, or after it, `wb` prints one line with that date on its standard
  error before the session starts (S12-platforms, "Development builds
  on macOS").
- On exit, `wb` prints a short summary of the session's returned work
  and the commands to review and land it, on its standard error in
  interactive and non-interactive use, so it never mixes into
  `claude`'s output on standard output, for example:

  ```
  wraith box: session myrepo-20261003-1412-k3f9 returned 3 commits (+ WIP)
    review: wb diff myrepo-20261003-1412-k3f9
    land:   wb land myrepo-20261003-1412-k3f9
    ⚠ flagged: package.json scripts changed, .github/workflows/ci.yml changed
    terminal: dropped 1 clipboard write, 2 notifications became a bell
    links:    Security guide  https://docs.example.com/security
              src/main.go     file:///…/myrepo/src/main.go  (guest path)
  ```

  A session that ended after its terminal closed, or after it was
  lost, has no `wb` left to print it, and `wb sessions` shows its
  summary. Each of several parallel sessions prints its own
  (FR05-parallel-sessions). When a session's changes stayed in the
  guest, after a deadline or a failed WIP commit or push, the summary
  and `wb sessions` give the worktree's path in the guest and
  `wb shell --project P` to reach it (S06-09-session-lifecycle, "Changes left in the guest"). When the VM's `wb-netd` or `wb-proxyd` was down
  during the session, the summary says so and for how long
  (S04-07-supervision).

- During an agent session, `wb` never prints approval prompts. That
  terminal shows the agent's output, which the guest controls, so any
  prompt there could be forged (SEC14-no-fake-approvals). Approvals arrive as native
  notifications, or are answered with `wb approve` from another terminal.

**Status:** Draft
