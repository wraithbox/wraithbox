# S05 - Command Line Interface

**Purpose:** Specify `wb`, the single user-facing command.

**Requirements:** FR01-drop-in to FR05-parallel-sessions, FR09-approve-unknown, FR12-clipboard, FR13-claude-state to FR17-same-everywhere, SEC03-no-host-exec, SEC10-audit, SEC14-no-fake-approvals, NFR01-startup, NFR06-explained-refusals.

## For review

- **Decides:** the shape of `wb` and its commands, and, since I37,
  that guest output reaching the user's terminal passes an allowlist
  filter in `wb` ("Terminal stream"), boundary 6 of T00-index.
- **You are approving:** the I37 and I30 decisions as spec text: the
  allowlist measured in X19-terminal-filter, tightened as the security
  review of PR66 asked. OSC 52 is dropped until I38, notifications
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
- **Open decisions:** 1. Reset the terminal on exit, rather than run
  the session on an alternate screen of `wb`'s own (recommended:
  reset). 2. What `wb` does with a stream that goes to a pipe or a
  file: pass it unchanged, drop only `ESC`-introduced sequences, or
  apply the full filter. This one has no recommendation, because
  passing the stream unchanged weakens SEC03-no-host-exec. Dropping `ESC`-introduced sequences
  changes only output that holds an `ESC`, which valid JSON can't. The
  full filter can also drop C1 characters and `DEL` from JSON. Until it
  is decided, the full filter applies.
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
  terminal (S12-platforms). A stream that goes to a pipe or a file is
  open decision 2, and gets the full filter until it is decided.
  Scripts read such a stream (`claude -p`), and a later `| cat` puts it
  on the terminal.
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

**Around the stream.** The title, the reset and the input drain apply
only when `wb`'s standard output is a terminal, and the drain also
needs standard input to be one. `wb claude -p … > out.txt` gets none
of them, whatever open decision 2 settles.

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
  scroll-back, unlike `claude` (FR01-drop-in, open decision 1).
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
`wb sessions`, `wb approve`, `wb audit`), it never passes a control
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
