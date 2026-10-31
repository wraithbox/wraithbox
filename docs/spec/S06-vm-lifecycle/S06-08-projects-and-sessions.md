# S06-08 - Projects and Sessions Inside a VM

**Purpose:** How projects map onto guest users and sessions onto
worktrees, and how Claude Code runs in the guest.

## Design

### Guest users

Each project gets a dedicated **guest** user account, created by
`wb-guestd`. Its home on the data disk holds:

- the project clone;
- Claude Code state (FR13-claude-state);
- tool caches.

Guest users can't read each other's homes (SEC08-proj-isolation). No
host user accounts are created (SEC12-least-privilege).

A home is mode 700, under the data volume's mount point
(S06-05-disks). Its path is set when the user is created, and never
changes, because macOS refuses to change it in the record
(X08-data-disk).

#### Removing a home

A home is removed when its project is removed (`wb project rm`), or
when an ephemeral user's session ends. `wb-hostd` sends `RemoveUser`
with the project and the user's UID, GID and home. `wb-guestd` takes it
only for one of these:

- the user it made or checked with that project since it started;
- after a restart, a record with that name, UID, primary group and
  home;
- after a system disk swap, with no record, a home whose owner is that UID.

With no record and no home it answers that the user is removed. A
request for a known user of another project is refused with
`project-user-unknown`. One removal of a user runs at a time, and
another is refused with `project-user-removal-running`.

Then `wb-guestd`:

1. locks the user (S13-01-process-hardening, "Locked users") and runs
   the kill loop (S13-01-process-hardening, "Per-user launchd
   services"). With processes left, it answers how many, changes
   nothing else, and `wb-hostd` asks again later;
2. removes the entries of the home in child processes that run as
   that user, in its own group only, for at most 15 seconds. So it never
   follows a link the user left with root's rights. The first gives the
   user every right on each of its directories, so a directory it set
   to mode 000 doesn't hide its entries, and the second removes them. A
   child that didn't start, ran past its deadline, or ended on a signal
   refuses the removal with `project-home-child-failed`, and the record
   stays as it was. The stop loop at a link drop leaves the processes
   of a user under removal alone, so it doesn't stop that child;
3. removes the home directory itself;
4. retires the record: the shell `/usr/bin/false` first, then
   `pwpolicy -disableuser` and hidden from the login window. A step
   that fails refuses the removal, and `wb-hostd` asks again. Because
   the shell goes first, `EnsureUsers` refuses a record that a failed
   step left half retired.

What that user can't remove stays, such as a file guest root left
there. `wb-guestd` logs it with `project-home-left` and still answers
that the user is removed. It stays in a directory no later user gets,
because the user's IDs are never reused (S06-03-image-updates,
"Allocation").

The record stays, locked and disabled, by design. The user's IDs are
never reused (S06-03-image-updates, "Allocation"), and the next system
disk starts the directory without it. This doesn't depend on whether
root can delete the record. In X17-image-build `sysadminctl -deleteUser`
left it, and in X08-data-disk (item 11) `opendirectoryd` refused its
deletion through `dscl` (I138). In the needs-vm run of PR441, root's
`dscl . -delete` through `sudo` removed one. A removal of a retired user runs the same steps, and answers that
it is removed again. `EnsureUsers` refuses a retired user, because its
shell differs.

So each removed project leaves one user record and one group record in
the VM's local directory, which grows by those two for the life of the
system disk. A system disk swap starts the directory again from the
image.

### Standard accounts

A project user is a standard account:

- outside the `admin` and `wheel` groups;
- with no sudoers rule.

Guest root can bind the host-guest socket port of `wb-guestd`. So a
project user must not reach root through `sudo`
(X27-vsock-confinement).

Packages come from the toolchain prefix, which only `wb-guestd` writes
(S06-01-image-layers, "Toolchain prefix", and X20-shared-homebrew).

### Worktree per session

Each session gets its own git worktree of the project clone, at
`<home>/sessions/<session-id>` in the project user's home. So parallel
sessions in one project don't collide (FR05-parallel-sessions).

Per-session build outputs, such as Xcode DerivedData, are kept inside
the worktree.

### Conversation history

Claude Code keeps its conversations per working directory, under
`~/.claude/projects/<directory>`. `<directory>` is the path, with each
character other than a letter or digit replaced by `-`. `claude
--continue` resumes "the most recent conversation in the current
directory" (`claude --help`, Claude Code 2.1.289).

Each session has a new worktree path. So before it starts `claude`,
`wb-guestd`, running as the project user:

1. makes that directory for the new worktree;
2. makes it a symbolic link to one directory per project,
   `~/.claude/projects/wb-project`.

`--continue` and `--resume` then find the conversations of the
project's earlier sessions (FR13-claude-state).

The link is made as the project user (S06-09-session-lifecycle, "As the project user"). So `wb-guestd` never follows a link the project user
made with root's rights.

`wb discard` removes the session's link with its worktree. It leaves
the per-project directory.

### Claude Code in the guest

S14-claude-in-guest specifies its install, the carry-in of the user's
configuration, and its permission mode (X01-model-credential,
B60-model-credential).

The image sets the Claude Code version, and the updater is off in the
guest. A new Claude Code version needs a new image. Building an image
for a new Claude Code version must be cheap next to a full `wb image
build` (I220).

#### The environment

`wb-guestd` starts `claude` with these variables in its environment
allowlist:

- the model credential's placeholder (S09-05-credentials, "Model credential"). It is in `CLAUDE_CODE_OAUTH_TOKEN` for a long-lived
  token, or in `ANTHROPIC_API_KEY` for an API key;
- `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`. Claude Code's
  documentation says it turns off updates, telemetry, error reporting
  and feature flag fetching (B45-claude-in-guest, "Evidence");
- `DISABLE_UPDATES=1`, which blocks every update path, `claude update`
  included (I45).

#### The variables aren't the control

The variables are meant to keep Claude Code from asking for the hosts
and paths that the model API profile leaves out. That is assumed, and
C06-model-api checks it.

The control is `wb-proxyd`. It refuses the event log, and every host
outside the profile, whatever the guest sets (S07-11-http-policy, "model API").

With the variable set, Claude Code doesn't fetch feature flags. So it
reaches `/api/eval/` only when `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`
is unset, as in the second pass of the C06-model-api run. The
profile's allow of `/api/eval/` stays, because the variable isn't the
control.

#### Plugins

Plugins from the marketplace are unavailable in the guest in V1,
because `downloads.claude.ai` is outside the profile.

### Ephemeral sessions

Ephemeral sessions use a throwaway guest user, removed at session end
(FR14-ephemeral).

## Decisions

### A new worktree path per session

- **Rejected:** one worktree path for every session. A parallel
  session, or an ended session that isn't discarded yet, still holds
  it.

**Status:** Draft
