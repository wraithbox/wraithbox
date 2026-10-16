# S05-01 - Agent Commands

**Purpose:** How `wb claude` passes its arguments to Claude Code, how it runs
interactive and non-interactive sessions, and what a session does.

## Design

### `wb claude`

```
wb [wb flags] claude [claude args]
```

Every argument after `claude` reaches Claude Code unchanged. That
includes arguments that look like `wb` flags:

- `wb claude --help` shows Claude Code's help.
- `wb claude --isolated` passes `--isolated` to Claude Code.

So `wb claude --resume`, `wb claude -p "…"` and `wb claude mcp list`
behave as `claude …` does. No current or future Claude Code flag or
subcommand can collide with Wraith Box (FR01-drop-in).

Users who want the bare word can alias it in their shell
(`alias claude='wb claude'`). Wraith Box doesn't install such an alias.

### Interactive and non-interactive use

Use is interactive when `wb`'s standard input and standard output are
both terminals.

- `wb` relays the TTY in raw mode, and `claude` runs on a PTY in the
  guest.
- Window-size changes are forwarded.
- Standard error goes to the PTY too. When `wb`'s standard error isn't
  a terminal, it is on its own channel instead.

Use is non-interactive when either isn't a terminal, such as a pipe, or
`claude -p … > out.txt`.

- Standard input and standard output are streamed.
- Standard error is on its own channel.

`wb` doesn't read `claude`'s arguments to decide. So `wb claude -p "…"`
in a terminal is interactive use, as `claude -p "…"` there has a
terminal. The exit status is in S05-02-exit-status-and-signals.

### Further agents

Further agents are further agent commands (`wb <agent> …`), behind the
same agent interface (V1-02-claude-only, S14-claude-in-guest, "Agent interface"). `claude` is the only one in v1.

S14-claude-in-guest says:

- how Claude Code gets into the guest;
- which of the user's `~/.claude/` goes with it;
- which Claude Code commands don't work in a guest.

### Session flags

These flags are valid only before an agent command. Before any other
command, they are a usage error.

| Flag | Meaning |
|---|---|
| `--isolated` | Run this session in the isolated VM, whatever the project's `placement` (SEC08-proj-isolation, S06-04-vms-and-placement) |
| `--ephemeral` | No persisted state for this session (FR14-ephemeral) |
| `--learn` | Learn mode (FR10-learn-mode), with the refusals below |
| `--guest <os>` | Guest OS for this project, where the host offers more than one (S12-platforms). Otherwise the project's configured or default guest |

`--learn` is refused:

- unless the project's `placement` is `work`. `config_trust` doesn't
  count;
- with `--isolated`;
- by `wb-hostd`, while a session of another project runs in the VM, a
  debug shell included (S07-17-approvals-and-learning).

While a learn-mode session runs, `wb-hostd` also refuses other
projects' sessions.

### Session behavior

#### Inside a git repository

`wb claude` must run inside a git repository. Otherwise `wb` exits with
255, with a message that says why: the return path is git
(S08-workspace-and-git).

#### Closing the terminal

Closing the terminal ends the session.

1. `claude` gets a hangup.
2. Its work is committed as WIP, and pushed as on a normal exit.

V1 has no detach and no `wb attach`. `wb claude --continue` starts a
new session at the host's `HEAD`, which resumes the project's most
recent conversation. The files the old session left uncommitted are
only in its WIP commit, and `wb land` brings them back. The states of a
session are in S06-09-session-lifecycle.

#### First run

The first run in a repository registers the project. It creates the
project's guest user and clone, then starts the session. Later runs
reuse them.

At registration, `wb` creates the project directory
`<data>/projects/<project-id>`, and the `export.git` in it. `wb-hostd`
can't write either (S08-02-export-repository).

#### Signing profile expiry

On macOS, `wb` prints one line on its standard error before the
session starts:

- when the expiry date of the signing profile in the helper bundles is
  30 days away or less;
- or after that date.

The line gives the date (S12-03-host-prerequisites, "Development builds on macOS").

#### Summary on exit

On exit, `wb` prints a short summary of the session's returned work,
and the commands to review and land it. The summary goes to its
standard error, in interactive and non-interactive use. So it never
mixes into `claude`'s output on standard output. For example:

```
wraith box: session myrepo-20261003-1412-k3f9 returned 3 commits (+ WIP)
  review: wb diff myrepo-20261003-1412-k3f9
  land:   wb land myrepo-20261003-1412-k3f9
  ⚠ flagged: package.json scripts changed, .github/workflows/ci.yml changed
  terminal: dropped 1 clipboard write, 2 notifications became a bell
  links:    Security guide  https://docs.example.com/security
            src/main.go     file:///…/myrepo/src/main.go  (guest path)
```

- A session can end after its terminal closed, or after it was lost.
  Then no `wb` is left to print the summary, and `wb sessions` shows
  it.
- Each of several parallel sessions prints its own
  (FR05-parallel-sessions).
- A session's changes can stay in the guest, after a deadline or a
  failed WIP commit or push. The summary and `wb sessions` then give
  the worktree's path in the guest, and `wb shell --project P` to reach
  it (S06-09-session-lifecycle, "Changes left in the guest").
- When the VM's `wb-netd` or `wb-proxyd` was down during the session,
  the summary says so, and for how long (S04-07-supervision).

#### No approval prompts

During an agent session, `wb` never prints approval prompts. That
terminal shows the agent's output, which the guest controls. So any
prompt there could be forged (SEC14-no-fake-approvals). Approvals
arrive as native notifications, or are answered with `wb approve` from
another terminal.

## Decisions

### Closing the terminal ends the session

- **Decided:** I46 (B46-session-lifecycle).

**Status:** Draft
