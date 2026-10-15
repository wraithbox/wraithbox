# S08-08 - Host Git

**Purpose:** Which git binary runs on the host, the oldest version Wraith Box
accepts, and where that is checked.

## Design

Git on the host parses data the guest built: `receive-pack` and
`index-pack` parse each push into `landing.git`, and `wb land` fetches
the same objects into the user's repository (SEC03-no-host-exec).
Wraith Box doesn't ship git, because git is GPL-2.0 and S10-tech-stack
allows only permissive licenses. It requires a minimum version instead
(B41-git-data-scope, item 4).

### Which git

`wb` and `wb-hostd` resolve the git binary the same
way, and never from `PATH`: the `git` setting in
`<config>/config.toml` when it is set, otherwise the platform's fixed
default, `/usr/bin/git` on macOS and Linux and
`C:\Program Files\Git\cmd\git.exe` on Windows. A `git` setting that
isn't an absolute path is refused. On macOS without the Command Line
Tools, `/usr/bin/git` is a stub that doesn't run git, so the version
check below fails there, and its message says to install the Command
Line Tools or set `git`. `wb` never runs a
binary that `wb-hostd` names. It resolves its own, compares it with
the path `wb-hostd` reports, and refuses on a mismatch, so a
compromised `wb-hostd` can't make `wb` run a program it wrote
(SEC12-least-privilege).

### Minimum version

Git 2.54.0, the oldest version the git spikes
ran (Apple git 2.54.0 in X07-git-round-trip and X28-git-alloc-limit),
as a constant in `wb` and `wb-hostd`. A git security release that
fixes code the round trip runs (`upload-pack`, `fetch`,
`receive-pack`, `index-pack`) raises it, by a change to this spec.

### Where it is checked

`wb-hostd` checks its git before it
confines itself (S04-architecture), and refuses every push and every
session start when the check fails. `wb` checks its own resolved git
in `wb setup`, at each session start and before each `wb land`. Each
check runs the absolute path as `git version` and reads
`git version <major>.<minor>.<patch>` from the start of the output.
After the patch number it accepts only the end of the line, a space
and a comment such as `(Apple Git-157)`, or `.windows.<n>`. Anything
else, a release candidate such as `2.54.0.rc1` included, counts as
output it can't read. Output it can't read, a failed run, or a
version below the minimum refuses, with a message that names the
binary, its version and the minimum (NFR06-explained-refusals), and
the decision goes to the audit log with the rule.

### Kept on top

The settings of `receive-pack` above (every object
check an error, the size limit, `GIT_ALLOC_LIMIT`) and the pack
scanner stay, whatever the version.

### Later

A receive side written in Go, in place of
`git receive-pack`, is an option for a later version, not V1.

**Status:** Draft
