# S08-08 - Host Git

**Purpose:** Which git binary runs on the host, the oldest version
Wraith Box accepts, and where that is checked.

## Design

Git on the host parses data the guest built (SEC03-no-host-exec):

- `receive-pack` and `index-pack` parse each push into `landing.git`;
- `wb land` fetches the same objects into the user's repository.

Wraith Box doesn't ship git. It requires a minimum version of the
host's git instead.

### Which git

`wb` and `wb-hostd` resolve the git binary the same way, and never from
`PATH`:

1. the `git` setting in `<config>/config.toml`, when it is set;
2. otherwise the platform's fixed default: `/usr/bin/git` on macOS and
   Linux, and `C:\Program Files\Git\cmd\git.exe` on Windows.

A `git` setting that isn't an absolute path is refused.

On macOS without the Command Line Tools, `/usr/bin/git` is a stub that
doesn't run git. The version check then fails there, and its message
says to install the Command Line Tools or set `git`.

`wb` never runs a binary that `wb-hostd` names. It resolves its own,
compares it with the path `wb-hostd` reports, and refuses on a
mismatch. So a compromised `wb-hostd` can't make `wb` run a program it
wrote (SEC12-least-privilege).

### Minimum version

The minimum is git 2.54.0, as a constant in `wb` and `wb-hostd`. That
is the oldest version the git spikes ran: Apple git 2.54.0 in
X07-git-round-trip and X28-git-alloc-limit.

A git security release that fixes code the round trip runs raises the
minimum, by a change to this spec. That code is `upload-pack`, `fetch`,
`receive-pack` and `index-pack`.

### Where it is checked

- `wb-hostd` checks its git before it confines itself
  (S04-architecture). It refuses every push and every session start
  when the check fails.
- `wb` checks its own resolved git in `wb setup`, at each session
  start, and before each `wb land`. Every other command that resolves
  a project from a directory, the `wb project` commands among them,
  checks it too, since it runs git in that directory.
- `wb` refuses a command that resolves a project from a directory when
  `wb-hostd` reports a failed check of its own git, or another git
  binary than `wb`'s. `wb-hostd` refuses only the session start
  itself. The stricter side in `wb` keeps one git for both processes.

Each check runs the absolute path as `git version`. It reads `git
version <major>.<minor>.<patch>` from the start of the output. After
the patch number it accepts only:

- the end of the line;
- a space and a comment, such as `(Apple Git-157)`;
- `.windows.<n>`.

Anything else counts as output it can't read, a release candidate such
as `2.54.0.rc1` included.

The check refuses on output it can't read, a failed run, or a version
below the minimum. The message names the binary, its version, and the
minimum (NFR06-explained-refusals). The decision goes to the audit log
with the rule.

### Kept on top

These stay, whatever the version:

- the settings of `receive-pack`: every object check an error, the
  size limit, and `GIT_ALLOC_LIMIT` (S08-03-pushes);
- the pack scanner (S08-04-push-checks).

## Decisions

### Require a minimum git, don't ship one

- **Why:** git is GPL-2.0, and S10-tech-stack allows only permissive
  licenses.
- **Decided:** B41-git-data-scope, item 4.

### `git receive-pack` in V1

A receive side written in Go, in place of `git receive-pack`, is an
option for a later version, not V1.

**Status:** Draft
