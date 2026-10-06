# S05 - Command Line Interface

**Purpose:** Specify `wb`, the single user-facing command.

**Requirements:** FR01-drop-in to FR05-parallel-sessions, FR09-approve-unknown, FR12-clipboard, FR13-claude-state to FR17-same-everywhere, SEC03-no-host-exec, SEC10-audit, SEC14-no-fake-approvals, NFR01-startup, NFR06-explained-refusals.

## For review

- **Decides:** the shape of `wb` and its commands, and, since I37,
  that guest output reaching the user's terminal passes an allowlist
  filter in `wb` ("Terminal stream"), boundary 6 of T00-index.
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
  session on an alternate screen of its own ("Reset on exit"), and a
  stream that goes to a pipe or a file passes the same full filter as
  terminal output ("Where").
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
    terminal: dropped 1 clipboard write, 2 notifications became a bell
    links:    Security guide  https://docs.example.com/security
              src/main.go     file:///…/myrepo/src/main.go  (guest path)
  ```

- During an agent session, `wb` never prints approval prompts. That
  terminal shows the agent's output, which the guest controls, so any
  prompt there could be forged (SEC14-no-fake-approvals). Approvals arrive as native
  notifications, or are answered with `wb approve` from another terminal.

## Terminal stream

The terminal emulator parses the guest's output and acts on some of it
on the host: OSC 52 writes the clipboard, iTerm2's OSC 1337 saves
files, kitty graphics can read a file by its path, and every parser
bug is a path to the host. So guest output is boundary 6 of T00-index,
and `wb` filters it with an allowlist that fails closed, as `wb-netd`
filters packets (SEC03-no-host-exec).

- **Where.** On every stream that `wb claude` or `wb shell` writes to a
  terminal. On WSL that is the WSL-side `wb`, which writes to the
  terminal (S12-platforms). A stream that goes to a pipe or a file
  passes the same full filter (decided on I37, 2026-10-06). Scripts
  read such a stream (`claude -p`), and a later `| cat` puts it on the
  terminal. The filter passes only output that is safe in a terminal,
  so a later `cat` of the file is as safe as the live session. One
  filter keeps one code path and one fuzz target, and keeps colors for
  `less -R` and CI logs. It passes text, Markdown and JSON unchanged
  when they are valid UTF-8 and don't hold a control character that
  the table below drops (C1, `DEL`, or a C0 control other than the ones it
  passes). Invalid bytes become U+FFFD, so binary output through
  standard output isn't supported. If moving files out of the guest is
  ever needed, it gets its own command, not an unfiltered stream.
  Rejected: passing the stream unchanged, which weakens
  SEC03-no-host-exec, and dropping only `ESC`-introduced sequences,
  which is a second filter to keep and fuzz.
- **How.** The filter splits the stream into text, controls and
  sequences, and passes a token only when a rule below allows it. It
  drops any other token whole, from its `ESC` to its end, so the
  terminal's parser is back in its ground state after every token. It
  never escapes a dropped sequence into visible text, which would move
  what the TUI drew.
- **Ends of a sequence.** A CSI ends at its final byte (`@` to `~`). A
  string sequence (OSC, DCS, APC, PM, SOS) ends at ST, written as
  `ESC \` or as U+009C, and an OSC also at BEL. CAN and SUB end any
  sequence, and so does an `ESC` inside a string sequence that doesn't
  start ST, which then begins the next token. The filter buffers at most 64 bytes of an unfinished CSI
  and 16 KB of an unfinished string sequence. Past that it discards
  everything up to the sequence's end, counted as one drop, so the tail
  of a long payload never shows as text.
- **Counts.** Each drop is counted by rule, and the session summary
  reports the counts (NFR06-explained-refusals). A sequence that a new
  Claude Code version starts to use shows up there.

| Class | Passes | Dropped |
|---|---|---|
| Text | valid UTF-8 | C1 controls (U+0080 to U+009F); invalid bytes become U+FFFD |
| C0 controls, DEL | BEL (at most one per second), BS, HT, LF, CR, SI | the rest, DEL included |
| `ESC` | `7`, `8`, `M`, `D`, `E`, `=`, `>`, `( B`, `( 0`, `) B`, `) 0` | the rest, `ESC c` included |
| CSI parameters | digits, `;` and `:`, at most 16 values of at most 5 digits, 64 bytes in all | any other |
| CSI screen | `A` to `H`, `f`, `` ` ``, `a`, `d`, `e`, `@`, `L`, `M`, `P`, `X`, `b`, `S`, `T`, `r`, `I`, `Z`, `g`, `m`; `J` and `K` with 0 to 2; `s` and `u` without parameters | `CSI 3 J`, which erases the host's scroll-back |
| CSI modes | `? h/l` for 25, 1000, 1002, 1003, 1004, 1006, 1016, 1049, 2004, 2026, 2031; `? … $ p` (mode query) | other modes |
| CSI keyboard | kitty keyboard `> u` (push, at most 4 deep per screen), `< u` (pop, only entries the guest pushed on that screen), `? u`; `> 4 ; 2 m` and `> 4 m`; `SP q`; `! p` | `= u`, which changes the current entry in place; other `> m` |
| CSI queries | `c`, `> c`, `5 n`, `6 n`, `> q`, `14 t`, `16 t`, `18 t` | other `t`: window moves, title reports, the title stack |
| OSC | 9 ; 4 (progress) with numeric parameters; 10, 11, 12 with `?` only; 9, 99 and 777 (notifications) become one BEL | the rest: 0 to 2 (title), 8 (links), 52, 1337, 7, 133, … |
| DCS, APC, PM, SOS | | all, whole |

The list is the one X19-terminal-filter measured with Claude Code
2.1.289, tightened as the security review of PR66 asked: modes the
recordings don't hold are left off, and each cap is above the largest
value recorded. OSC 10 to 12 queries stay because the Claude Code
binary holds the background color query. Queries pass because the
reply goes to the guest, and Claude Code uses them to learn what the
terminal supports. What a reply tells the guest is T09-terminal-fingerprint.
The kitty keyboard protocol keeps one stack per screen, so `wb` counts
the guest's pushes on the main and the alternate screen apart, and
drops a pop that would reach an entry the user's shell pushed. The
title report (`CSI 21 t`), OSC 52 with `?`, and DECRQSS (a DCS) never
join the list: each makes the terminal type guest-chosen text back
into its input, which would reach the user's shell after `wb` exits.

**Links.** OSC 8 is dropped and its text stays as plain text (H1,
I30). A link's text can differ from its target, and a link to
`localhost` or a private address names a service on the host, not in
the guest. Terminals still make a bare URL clickable. `wb` keeps each
dropped link's URL (at most 2048 bytes) and text (at most 256
characters), records them in the audit log through `wb-hostd`, and
lists them, without repeats, in the session summary. It keeps at most
100 entries. Past that it counts the rest by mark, and the summary says
"and N more". An entry is marked when the URL is `file:` (a guest
path), an IP literal, `localhost` or a name under `.localhost`,
`.local`, `.internal`, `.home.arpa` or `.lan`, a name without a dot, a
URL with a user name, `http:`, or another scheme. The mark is a
heuristic on the name alone: an unmarked name can still resolve to a
host-local address, so no mark doesn't mean safe.
Rejected: rewriting a link inline as `text (url)`, which changes line
widths the TUI has laid out; passing `https://` links to public hosts,
which leaves the text-target mismatch; and passing a link only when
its text equals its URL, which makes the filter follow the screen.

**Around the stream.** What `wb` itself writes to the terminal (the
window title, the reset on exit, and the `CSI ? 6 n` sentinel query of
the input drain) happens only when `wb`'s standard output is a
terminal, and the drain also needs standard input to be one (decided
on I37, 2026-10-06). `wb claude -p … > out.txt` gets none of them, and
its output still passes the filter ("Where").

- **Title.** `wb` saves the user's window title on the terminal's
  title stack (`CSI 22 t`), sets its own (`wb: <session id>`), and
  restores the saved one on exit (`CSI 23 t`). A guest title would
  reach desktop notifications through the bell, as iTerm2's
  Notification Center alerts and Ghostty's `bell-features = title` do
  (SEC14-no-fake-approvals).
- **Reset on exit.** After the relay ends and before `wb` prints
  anything, it resets what the guest may have changed: colors and
  style, the alternate screen, mouse modes, bracketed paste, focus
  events, color scheme reports, synchronized output, cursor visibility
  and style, the scroll region, the character sets, the kitty keyboard
  stacks (the guest's pushes popped, the alternate screen's before
  leaving it), `modifyOtherKeys` (`CSI > 4 m`), the keypad mode
  (`ESC >`), tab stops (`CSI ? 5 W`), and the progress indicator.
  Then it starts a new line. Without it, the guest can print a fake
  summary in the background color and hide the real flags that
  SEC03-no-host-exec relies on. The summary is advisory, and `wb diff`
  is authoritative. Not `ESC c`, which can clear the scroll-back.
  Rejected: running the session on an alternate screen of `wb`'s own,
  which resets everything but doesn't leave the session's output in the
  scroll-back, unlike `claude` (FR01-drop-in, decided on I37,
  2026-10-06).
- **Input drain.** Before restoring cooked mode, `wb` sends a sentinel
  query that the allowlist never passes, `CSI ? 6 n` (extended cursor
  position), and reads and discards input until the reply of that form
  (`CSI ? … R`) arrives or 100 ms pass. Terminals answer in order, so
  every reply to a guest query comes first, and a guest `CSI 6 n` can't
  end the drain early. A late reply to a guest query doesn't reach the
  user's shell. Input to the guest is not filtered.
- **Environment.** The guest session gets these host variables and no
  others: `TERM`, `TERM_PROGRAM`, `TERM_PROGRAM_VERSION`, `COLORTERM`,
  `LANG`, `LC_ALL`, `LC_CTYPE`. Claude Code picks its sequences from the
  first four and from query replies. The locale ones keep the guest's
  character encoding the same as the host's, also on a host that sets
  `LC_ALL` or `LC_CTYPE` without `LANG`. `LC_ALL` also sets the
  formatting categories, as it does on the host. The other `LC_*`
  variables, which set one formatting category each (`LC_TIME`,
  `LC_NUMERIC`, …), are left out. Not `TMUX` or `STY`,
  which make Claude Code wrap sequences in DCS, and not `SSH_*`,
  `TERMINFO`, `LC_TERMINAL` or `ITERM_SESSION_ID`. The window size
  crosses as resize messages.

**Guest text elsewhere.** Wherever else `wb` prints text the guest
influenced (the session summary and its link list, `wb diff`,
`wb sessions`, `wb approve`, `wb audit`, `wb learn report`,
`wb status`), it never passes a control
character, an escape sequence, or a Unicode format character
(bidirectional controls, zero-width characters). `wb diff`, logs and
audit records show them escaped, where a reviewer sees them. Other
output removes them (SEC10-audit, SEC14-no-fake-approvals).

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
| `wb trust [<repo>] [--project P]` / `wb untrust [<repo>] [--project P]` | Set or clear the project's `config_trust`, which allows repository-supplied configuration (SEC09-host-policy). `<repo>` (default: the current directory) resolves to its project by the rules of "Naming". It doesn't change the VM |
| `wb project show [--project P]` | Project id, name, location, recorded identity (remote URLs, root commits), `placement` and `config_trust` |
| `wb project move <dir> [--project P]` | Point the project at the repository in `<dir>`, keeping its id, state, approvals and policy ("Naming") |
| `wb project rename <name> [--project P]` | Change the project's name; the id stays |
| `wb project place work\|isolated [--project P]` | Set the project's `placement` (S06-vm-lifecycle, "VMs"). In a repository that isn't a project yet, it registers one first, so a new clone can start isolated |
| `wb project confirm [--project P]` | Accept the repository's current remote URLs and root commits as the project's, and clear `config_trust` ("Naming") |
| `wb project rm [--project P]` | Remove a project and everything Wraith Box holds for it ("Naming") |
| `wb cred set/list/rm <binding>` | Manage credentials held by `wb-proxyd` (S09-policy-credentials-audit) |
| `wb audit tail/search` | Read the audit log (SEC10-audit) |
| `wb vm start/stop/suspend/status` | Explicit VM control |
| `wb image build/list/use` | Base image management (S06-vm-lifecycle) |
| `wb shell [--project P]` | Debug shell as the project user, labeled as a debug shell |
| `wb setup` | Check host prerequisites (S12-platforms) and the minimum git version (S08-workspace-and-git), install and start the per-user services |
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
  (S08-workspace-and-git, "Out of the guest"). It keeps only `HOME`,
  `XDG_CONFIG_HOME` from the login environment when it is set,
  `TMPDIR`, and `LC_ALL=C`. Unlike `wb-git` it reads the user's global
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
    `wb untrust`, and the `wb project` commands without `--project`.
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
- Moving: `wb project move <dir>` changes the key and keeps the id. The
  project's Claude Code state (FR13-claude-state), approvals, policy
  and landing repository stay with it. It runs the identity comparison
  for `<dir>` and prints any difference, and the next session start
  then refuses until `wb project confirm`. A move to a key that another
  project has is refused, with that project named.
- Registration: the first session in an unknown key registers a new
  project. `wb` first compares the repository's identity (the same
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
  in `state.db`. The secret store items behind its bindings stay,
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
  wasn't landed or discarded, and the refusal lists them. Branches
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
