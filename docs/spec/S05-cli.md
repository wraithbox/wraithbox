# S05 - Command Line Interface

**Purpose:** Specify `wb`, the single user-facing command.

**Requirements:** FR01-drop-in to FR05-parallel-sessions, FR09-approve-unknown, FR12-clipboard, FR13-claude-state to FR17-same-everywhere, SEC03-no-host-exec, SEC10-audit, SEC14-no-fake-approvals, NFR01-startup, NFR06-explained-refusals.

## For review

- **Decides:** the shape of `wb` and its commands, and, since I37,
  that guest output reaching the user's terminal passes an allowlist
  filter in `wb` (S16-terminal-stream), boundary 6 of T00-index. Since
  I46, closing the terminal ends the session, and "Exit status and
  signals" says what `wb claude` exits with and which signals it
  forwards (B46-session-lifecycle).
- **You are approving:** the I37 and I30 decisions as spec text: the
  allowlist measured in X19-terminal-filter, tightened as the security
  review of PR66 asked. OSC 52 is dropped, because V1 has no clipboard
  (V1-08-no-forward-clipboard), notifications
  become a bell, and links are dropped on screen and listed in the
  session summary. `wb` sets the window title, resets the terminal
  before the summary, and passes seven environment variables to the
  guest. One change from the I37 decision, which says `wb` strips
  control sequences from guest text outside the stream: `wb diff`, logs
  and audit records show them escaped instead, where a reviewer sees
  them (X19-terminal-filter asks for escaping in logs). Other output still
  removes them.
- **Controls touched:** SEC03-no-host-exec (nothing the agent produces
  runs on the host without the user) and SEC14-no-fake-approvals
  (approvals can't be spoofed by the guest), both kept and extended to
  the terminal. FR12-clipboard (clipboard opt-in, with approval) is
  kept, because OSC 52 never passes. FR01-drop-in (`wb claude` behaves
  as `claude`) holds, except that `/copy`, notification text, links,
  and Claude Code's own window title are lost.
- **Assumed:** that a headless emulator shows what Ghostty and
  Terminal.app show (I62 checks it by eye), that both answer the
  drain's `CSI ? 6 n`, and that the caps fit later Claude Code
  versions. Drop counts show it when they don't.
- **Open decisions:** none. The maintainer decided both on I37 on
  2026-10-06: `wb` resets the terminal on exit rather than run the
  session on an alternate screen of its own (S16-terminal-stream,
  "Reset on exit"), and a stream that goes to a pipe or a file passes
  the same full filter as terminal output (S16-terminal-stream,
  "Where").
- **Brief:** B37-terminal-boundary

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
- Interactive use, when `wb`'s standard input and standard output are
  both terminals: raw-mode TTY relay, and `claude` runs on a PTY in the
  guest. Window-size changes are forwarded. Standard error goes to the
  PTY too, unless `wb`'s standard error isn't a terminal, in which case
  it is on its own channel. Non-interactive use, when either isn't a
  terminal (a pipe, or `claude -p … > out.txt`): stdin and stdout
  streamed, stderr on its own channel. `wb` doesn't read `claude`'s
  arguments to decide, so `wb claude -p "…"` in a terminal is
  interactive use, as `claude -p "…"` there has a terminal. The exit
  status is in "Exit status and signals".
- Users who want the bare word can alias it in their shell
  (`alias claude='wb claude'`). Wraith Box does not install such an alias.
- Further agents are further agent commands (`wb <agent> …`) behind the
  same agent interface (V1-02-claude-only). `claude` is the only one in v1.

### Session flags

Valid only before an agent command; given before any other command they
are a usage error:

| Flag | Meaning |
|---|---|
| `--isolated` | Run this session in the isolated VM, whatever the project's `placement` (SEC08-proj-isolation, S06-vm-lifecycle, "VMs") |
| `--ephemeral` | No persisted state for this session (FR14-ephemeral) |
| `--learn` | Learn mode; refused unless the project's `placement` is `work`, and refused with `--isolated` (FR10-learn-mode). `config_trust` doesn't count. `wb-hostd` also refuses it while a session of another project, a debug shell included, runs in the VM, and refuses other projects' sessions while it runs (S07-egress-gateway, "Approvals and learning") |
| `--guest <os>` | Guest OS for this project, where the host offers more than one (S12-platforms); otherwise the project's configured or default guest |

### Global flags

| Flag | Meaning |
|---|---|
| `-C <dir>` | Act as if `wb` was started in `<dir>` |
| `--help`, `-h` | Help |
| `--version` | Version |

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
  session are in S06-vm-lifecycle, "Session lifecycle".
- First run in a repository registers the project, creates its guest
  user and clone, then starts the session. Later runs reuse them.
  At registration `wb` creates the project directory
  `<data>/projects/<project-id>` and the `export.git` in it, because
  `wb-hostd` can't write either (S08-workspace-and-git, "Export
  repository").
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
  `wb shell --project P` to reach it (S06-vm-lifecycle, "Changes left
  in the guest"). When the VM's `wb-netd` or `wb-proxyd` was down
  during the session, the summary says so and for how long
  (S04-architecture, "Supervision and failure").

- During an agent session, `wb` never prints approval prompts. That
  terminal shows the agent's output, which the guest controls, so any
  prompt there could be forged (SEC14-no-fake-approvals). Approvals arrive as native
  notifications, or are answered with `wb approve` from another terminal.

### Exit status and signals

`wb claude` exits as `claude` did, and with 255 when Wraith Box
failed, as `ssh` "exits with the exit status of the remote command or
with 255 if an error occurred" (`man ssh`):

| Outcome | `wb` exits with |
|---|---|
| `claude` exited with code n, and its work was returned | n |
| `claude` was ended by signal n, and its work was returned | 128 + n, as a shell reports it |
| a usage error in the `wb` flags | 2 |
| any other failure of Wraith Box: a refusal before the session starts, a lost session (S06-vm-lifecycle, "Session lifecycle"), a WIP commit or push that failed, an exit status from the guest out of range | 255 |

`claude` can exit with 2 or 255 itself. When the code comes from Wraith
Box, the last line `wb` writes to standard error starts with
`wraith box:` and says what failed. When the work wasn't returned, that
line also gives `claude`'s own exit status. A script then never takes
work that didn't reach the host for returned work.

Signals:

- **From the keyboard.** In interactive use `Ctrl-C`, `Ctrl-\` and `Ctrl-Z`
  are bytes in the terminal stream, and the PTY in the guest turns
  them into signals for `claude`, as a local terminal does. A window
  size change (`SIGWINCH`) becomes a resize message.
- **Hangup.** `SIGHUP` to `wb` ends the session ("Session behavior").
  `wb` still waits for the end, writes nothing more to the terminal,
  and exits as the table says. When `wb` is killed instead, `wb-hostd`
  sees its connection close and hangs up the session the same way.
- **Forwarded.** `wb` forwards `SIGINT`, `SIGQUIT` and `SIGTERM` that
  it receives to `claude`'s process group, in interactive and
  non-interactive use, and keeps waiting for `claude` to exit. Other
  signals have their default effect on `wb`. One that ends it ends the
  session as a hangup does.
- **Job control.** When `claude` stops itself in interactive use, as
  it does on `Ctrl-Z`, the session helper in the guest sees it
  (S06-vm-lifecycle, "Session helper"), and `wb-guestd` reports it.
  The report is guest input, and it can only make `wb` give the
  terminal back early. `wb` then resets the terminal and drains its
  input as on exit (S16-terminal-stream, "Around the stream"),
  restores the user's window title, and stops itself with `SIGSTOP`, so the user's shell has the
  terminal again. On `SIGCONT` (`fg`), `wb` sets its title and raw
  mode again and has `wb-guestd` continue `claude`, which redraws. A
  `SIGTSTP` sent to `wb` from outside, which a terminal in raw mode
  never sends, gets the same steps, after `wb` has `wb-guestd` stop
  `claude`'s process group. In non-interactive use, `wb` forwards
  `SIGTSTP` to `claude` before it stops itself, and `SIGCONT` after
  it continues. A stopped `wb` keeps its connection to `wb-hostd`, so
  the session stays. That job control works through the helper is not
  checked yet (I135).
- **A closed output.** `wb` ignores `SIGPIPE`, and an `EPIPE` on its
  standard output in non-interactive use (`wb claude -p … | head -1`)
  is how it learns that the reader closed its end. `wb-guestd` then closes
  `claude`'s standard output, so `claude` sees a broken pipe as it
  would on the host, and `wb` exits as the table says for `claude`'s
  exit.

## Terminal stream

The guest output that `wb claude` and `wb shell` write passes an
allowlist filter in `wb`, boundary 6 of T00-index
(SEC03-no-host-exec). S16-terminal-stream specifies the filter and
everything else `wb` does at that boundary.

## Management commands

| Command | Purpose |
|---|---|
| `wb status` | VMs, their state, running sessions, pending approvals, and each host process with its state, version, restarts and last exit reason (S04-architecture, "Logs and health") |
| `wb sessions [--project P]` | List sessions and their returned work |
| `wb diff <session>` | Review returned work with flagged paths highlighted |
| `wb land <session> [--branch NAME]` | Fetch returned work into the host repo as a branch; never checks out or merges |
| `wb discard <session>` | Drop returned work and the session's guest worktree with its history link. A lost session ends with no recovery push (S06-vm-lifecycle, "Discarding a lost session") |
| `wb approve <id>` / `wb deny <id>` | Answer the pending approval request with that id (notification alternative). Without an id, refused with an error that lists the pending ids. An id that doesn't name an open request is refused with `approval-stale` (S09-policy-credentials-audit, "Approval flow") |
| `wb allow <host> [--project P]` | Add an allowlist entry (SEC05-default-deny) |
| `wb policy show/explain/edit [--project P]` | Effective policy and why a request was allowed or refused (NFR06-explained-refusals) |
| `wb learn report [--project P]` | Suggested allowlist from a learn-mode session (FR10-learn-mode) |
| `wb trust [<repo>] [--project P]` / `wb untrust [<repo>] [--project P]` | Set or clear the project's `config_trust`, which allows repository-supplied configuration (SEC09-host-policy). `<repo>` (default: the current directory) resolves to its project by the rules of "Naming". It doesn't change the VM |
| `wb project show [--project P]` | Project id, name, location, recorded identity (remote URLs, root commits), `placement` and `config_trust` |
| `wb project move <dir> [--project P]` | Point the project at the repository in `<dir>`, keeping its id, state, approvals and policy ("Naming") |
| `wb project rename <name> [--project P]` | Change the project's name; the id stays |
| `wb project place work\|isolated [--project P]` | Set the project's `placement` (S06-vm-lifecycle, "VMs"). In a repository that isn't a project yet, it registers one first by the rules of "Naming" (moved-project refusal, second-clone placement), so a new clone can start isolated |
| `wb project confirm [--project P]` | Accept the repository's current remote URLs and root commits as the project's, and clear `config_trust` ("Naming") |
| `wb project rm [--project P]` | Remove a project and everything Wraith Box holds for it ("Naming") |
| `wb cred set/list/rm <binding>` | Manage the binding credentials in the secret store, which `wb-proxyd` fetches when proxying needs them (S09-policy-credentials-audit, S15-least-privilege) |
| `wb audit tail/search` | Read the audit log (SEC10-audit) |
| `wb gate reset go-clock` | After a `go-sumdb-inconsistent` finding, print its two tree heads and their receipt times and ask for confirmation. Then `wb-hostd`, asked over local IPC, deletes the finding and the Go clock's stored tree heads, writes a 5019 event naming the command and the finding, and restarts `wb-proxyd`. The dependency gate then allows Go downloads again from a fresh head. The restart resets every proxied stream of both VMs, `claude` API streams included, starts a new wait of up to 30 s for the first point, and doesn't count toward the restart limit. Refused without a terminal, because a person has to confirm. It is never part of a guest RPC (S07-egress-gateway, "Dependency gate") |
| `wb vm start/stop/suspend/status` | Explicit VM control. `stop` and `suspend` are refused while the VM has a session that is starting, running, paused or ending, and `suspend` also while a kill of a project user is pending or after a leftover-process result (S06-vm-lifecycle, "Leftover processes"). The refusal names each one, the process ID of its `wb`, and how it ends: close its terminal, or wait for its deadline, and `wb sessions` lists them (S06-vm-lifecycle, "Idle suspend") |
| `wb image build/list/use` | Base image management (S06-vm-lifecycle) |
| `wb shell [--project P]` | Debug shell as the project user, labeled as a debug shell |
| `wb setup` | Check host prerequisites (S12-platforms) and the minimum git version (S08-workspace-and-git), create the projects root `<data>/projects` and its probe directory, which `wb-hostd` can't write (S04-architecture, "Each host daemon is self-sandboxed"), then install and start the per-user services |
| `wb help`, `wb version` | Help and version |

Each command has its own flags after the command name. Names of
commands are reserved: a new agent command may not reuse one.

`wb trust`, `wb untrust` and each `wb project` command that changes a
project (`move`, `rename`, `place`, `confirm`, `rm`) write a Device
Config State Change (5019) event with the project and the values
before and after (SEC10-audit, S09-policy-credentials-audit, "Audit").

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

- Project key: the host realm and the absolute path of the
  repository's git common directory
  (`git rev-parse --path-format=absolute --git-common-dir`). Every
  `git worktree` of one repository has the same common directory, so
  they are one project. A submodule has a common directory of its own
  (under the parent repository's `modules/`), so it is a project of its
  own. The remote URLs aren't part of the key, so a changed or added
  remote, or a token in a URL, doesn't make a new project.
  - *Realm.* The native host, or a WSL distribution by name. A
    repository on a Windows drive opened from WSL through `/mnt/c` and
    the same repository opened from native Windows are in two realms,
    so they are two projects.
  - *Path.* The key uses the path as git returns it, and `wb` doesn't
    recompute it. On macOS git's absolute common directory already
    resolves symbolic links and folds letter case to the spelling on
    disk (APFS), so `~/Git/foo` and `~/git/foo` give one key. On other
    platforms the canonical form comes from `internal/platform`
    (S12-platforms).
- Resolving the key: `wb` runs git for the key, the remotes and the
  root commits with the git binary of S08-workspace-and-git ("Host
  git"), `core.hooksPath=/dev/null` on its command line, and an
  otherwise scrubbed environment, as `wb-git` runs git
  (S08-workspace-and-git, "Out of the guest"). It sets only `HOME`,
  taken from the user database (the account's home directory), not
  from the environment, `XDG_CONFIG_HOME` from the login environment
  when it is set, `TMPDIR`, and `LC_ALL=C`. Unlike `wb-git` it reads the user's global
  git configuration, so remote URLs resolve as the user's git resolves
  them (`url.<base>.insteadOf`). So no `GIT_*` variable, from the user's
  shell or a `.envrc`, can point the directory at another repository
  or add configuration.
  - *Which directory.* `wb` walks up from the current directory to the
    first directory that holds a `.git` entry, as git does. That
    directory is the one the key must belong to.
  - *A `.git` file.* When that entry is a file (`gitdir: …`) instead
    of a directory, `wb` accepts it only for a linked worktree or a
    submodule. A linked worktree has `--git-dir` equal to
    `<common>/worktrees/<name>`, and that worktree's back-link,
    `<common>/worktrees/<name>/gitdir`, names this `.git` file. A
    submodule has `--git-dir` equal to the common directory, and its
    `core.worktree`, resolved relative to `--git-dir`, canonicalizes
    to the directory that holds this `.git` file. `wb` doesn't use
    `--show-toplevel` for this, because git derives it from that same
    `core.worktree`. Anything else, such as a `.git` file in a
    downloaded archive that names another project's repository or one
    of its submodules, refuses the command.
  - *Failure.* Any failed `rev-parse`, "dubious ownership" included,
    refuses the command. Every refusal here says why
    (NFR06-explained-refusals), and nothing is registered.
  - *Which commands.* Every command that resolves a project from a
    directory resolves it this way: `wb claude`, `wb trust`,
    `wb untrust`, the `wb project` commands without `--project`,
    `wb project move` for its `<dir>`, also when `--project` is given,
    and `wb land` and `wb diff`, which find the user's repository
    only this way, never from a path in `state.db` or from `wb-hostd`.
    `wb land` and `wb diff` refuse a session that isn't the resolved
    project's (S08-workspace-and-git, "Landing on the host").
  - *Wraith Box's own directories.* A key under `<data>`, `<config>`
    or `<logs>` refuses the command, so no command treats a repository
    that `wb-hostd` can write, such as a `landing.git`, as the user's.
- Project id: 128 random bits at registration (FR02-any-repo), written
  as 32 lowercase hexadecimal characters (the project ID format), kept
  for the project's life. The id isn't derived from the key, so a new
  repository at a moved project's old path never gets its id. The
  `projects` table of `state.db` (S04-architecture, "Host state") maps
  each key to one id, and a key has at most one project.
- Recorded identity: at registration `wb` records every URL of each
  remote as the user's git resolves it, after the cleaning of
  S08-workspace-and-git ("Remote URLs"). An SSH remote is compared as
  its `https://host/path` form, without user, port, password, query or
  fragment, so a switch between SSH and HTTPS for the same repository,
  or a rotated token, isn't a difference. A URL the guest doesn't get
  is compared with its user name, password, query string and fragment
  removed. For a repository with no remote, `wb` also records its root
  commits (`git rev-list --max-parents=0 --all`). It doesn't record
  them for a repository with a remote, because the walk reads the whole
  history at every session start, and the URLs already tell repositories
  apart.
- Grow-only: at a session start where the recorded identity is empty
  and the repository now has a remote or a root commit, `wb` records
  it, with a 5019 event. A repository that has had neither a remote
  nor a commit at any session start has nothing recorded, so a
  replacement at its path before then is the same project.
- Checked at each session start, and by every other command that
  resolves the project from a directory except `wb project confirm`
  and `wb project rm`: `wb` compares the recorded identity
  with the repository's current one. A difference in the URLs, or a
  recorded root commit that is no longer among the current root
  commits, refuses the command, and the message names what was
  recorded, what is there now and `wb project confirm`
  (NFR06-explained-refusals). Without this check a different repository
  cloned into the same directory would inherit the old project's
  approvals and policy.
- `wb project confirm` records the current identity and clears
  `config_trust`, so a replaced repository's `.wraithbox/` isn't read
  until the user runs `wb trust` and its boundary check again
  (SEC09-host-policy). It prints the `placement` and the approvals the
  project keeps, and its 5019 event holds both URL and root commit
  sets.
- Moving: `wb project move <dir>` resolves the key of `<dir>` by the
  rules above, then changes the project's key to it and keeps the id. The
  project's Claude Code state (FR13-claude-state), approvals, policy
  and landing repository stay with it. It runs the identity comparison
  for `<dir>` and prints any difference, and the next session start
  then refuses until `wb project confirm`. A move to a key that another
  project has is refused, with that project named.
- Registration: the first command that registers an unknown key (a
  session start, or `wb project place`) registers a new project, by
  the same rules for both. `wb` first compares the repository's identity (the same
  URLs, or for a repository without a remote, a shared root commit)
  with every existing project's.
  - *Moved.* When it matches a project whose location no longer
    resolves to its key, `wb` refuses the command and names that
    project with `wb project move`, which keeps its state, and
    `wb project rm`, which drops it.
  - *Second clone.* Otherwise a match is a second clone of a
    repository whose first clone is still in place, and it registers
    as its own project. The registration notice names each matching
    project and its `placement` (NFR06-explained-refusals). When any
    of them has `placement = isolated`, the new project starts with
    `placement = isolated` too, so a clone of a repository the user
    isolated doesn't land in the work VM (T13-new-project-grants).
    Only the placement is copied, never approvals, bindings, policy or
    `config_trust`.
- `wb project rm` removes the project: its settings, policy and the
  credential bindings in it, approvals, cached join approvals and the
  learn list that name it, `export.git` and `landing.git`, and its row
  in `state.db`. `wb` removes the project directory
  `<data>/projects/<project-id>` with both repositories itself, because
  `wb-hostd` can't remove it (S04-architecture, "Each host daemon is
  self-sandboxed"). The secret store items behind its bindings stay,
  because `wb cred` manages them and another project may use the same
  item. `rm` lists them, and `wb cred rm` removes them. It asks
  `wb-guestd` in each VM to remove the project user and its home. The
  pending removal is a record in `state.db` that survives restarts,
  and `wb-hostd` sends it when the VM runs, otherwise at its next
  start. `wb-guestd`'s reply is untrusted (SEC11-root-gains-nothing):
  the record stays until a reply for that project comes, and a reply
  never clears another project's record. A false reply can only leave
  the home inside that guest. The host-side state is gone before the
  request is sent, and ids are random, so no later project gets this
  one's id. `rm` is
  refused while the project has a session, or returned work that
  wasn't landed or discarded, and the refusal lists them. A lost
  session doesn't count as a session here: `rm` ends it with no
  recovery push (S06-vm-lifecycle, "Discarding a lost session"). Branches
  already landed in the user's repository stay. When a project holds a key or identity
  that another project should have, the user removes it with
  `wb project rm` and then moves the other one. `wb project move`
  never replaces a project, so dropping a project's state and
  approvals always takes this one explicit command.
- Name: a human-readable name is derived from the directory that holds
  the common directory, and `wb project rename` changes it.
- The maintainer decided project identity on I42
  (B42-trust-placement).
- Session id: `<project-name>-<yyyymmdd>-<hhmm>-<4 random chars>`.
- Returned work lands on host branches `wb/<session-id>` by default.

**Status:** Draft
