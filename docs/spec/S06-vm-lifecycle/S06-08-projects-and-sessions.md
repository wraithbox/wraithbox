# S06-08 - Projects and Sessions Inside a VM

**Purpose:** How projects map onto guest users and sessions onto worktrees, and
how Claude Code runs in the guest.

## Design

### Guest users

Each project gets a dedicated **guest** user account, created by
`wb-guestd`; its home on the data disk holds the project clone, Claude
Code state (FR13-claude-state), and tool caches. Guest users cannot read each other's
homes (SEC08-proj-isolation). No host user accounts are created (SEC12-least-privilege).
A home is mode 700, under the data volume's mount point (S06-05-disks),
and its path is set when the user is created and never changes,
because macOS refuses to change it in the record (X08-data-disk).
When a project is removed (`wb project rm`), or an ephemeral user's
session ends, `wb-guestd` removes that user's home in a child process
running as that user, so it never follows a link the user left with
root's rights. What that user can't remove, such as a file guest root
left there, stays, in a directory no later user gets, because the
user's IDs are never reused (S06-03-image-updates, "Updates"). Removing the record itself
is I138.

### Standard accounts

A project user is a standard account, outside the `admin` and
`wheel` groups, with no sudoers rule. Guest root can bind
the host-guest socket port of `wb-guestd`, so a project user must not
reach root through `sudo` (X27-vsock-confinement). Packages come from
the toolchain prefix, which only `wb-guestd` writes (S06-01-image-layers, "Toolchain prefix", X20-shared-homebrew).

### Worktree per session

Each session gets its own git worktree of the project clone, at
`<home>/sessions/<session-id>` in the project user's home, so
parallel sessions in one project do not collide (FR05-parallel-sessions). Per-session build
outputs (for example Xcode DerivedData) are kept inside the worktree.

### Conversation history

Claude Code keeps its conversations per working directory:
`claude --continue` resumes "the most recent conversation in the
current directory" (`claude --help`, Claude Code 2.1.289), under
`~/.claude/projects/<directory>`, where `<directory>` is the path
with each character other than a letter or digit replaced by `-`.
Each session has a new worktree path, so before it starts `claude`,
`wb-guestd`, running as the project user, makes that directory for
the new worktree a symbolic link to one directory per project,
`~/.claude/projects/wb-project`. `--continue` and `--resume` then
find the conversations of the project's earlier sessions
(FR13-claude-state). The link is made as the project user (S06-09-session-lifecycle, "As the project user"), so `wb-guestd` never follows a
link the project user made with root's rights. `wb discard` removes
the session's link with its worktree, and leaves the per-project
directory. Parallel sessions share that directory, so `--continue` in
one can pick the conversation another session is running, and two
sessions can then write one conversation. Whether Claude Code allows
that is open. The layout was read on a macOS host, not checked in a
guest, and I135 checks it there. I135 also checks Claude Code's
prompt to trust a folder, which a new worktree path per session may
bring up at every session start, unlike `claude` (FR01-drop-in).
Rejected: one worktree path for every session, which a parallel
session, or an ended session that isn't discarded yet, still holds.

### Claude Code in the guest

(X01-model-credential,
B60-model-credential). S14-claude-in-guest specifies its install, the
carry-in of the user's configuration, and its permission mode. The
image sets the Claude Code version, and the updater is off in the
guest. `wb-guestd` starts `claude` with these variables in its
environment allowlist:
- the model credential's placeholder, in `CLAUDE_CODE_OAUTH_TOKEN`
  for a long-lived token or `ANTHROPIC_API_KEY` for an API key
  (S09-04-credentials, "Model credential");
- `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, which Claude Code's
  documentation says turns off updates, telemetry, error reporting
  and feature flag fetching (B45-claude-in-guest, "Evidence");
- `DISABLE_UPDATES=1`, which blocks every update path, `claude update`
  included (I45).

The variables are meant to keep Claude Code from asking for the hosts
and paths the model API profile leaves out, which is assumed, and
C06-model-api checks it. They aren't the control:
`wb-proxyd` refuses the event log and every host outside the profile
whatever the guest sets (S07-egress-gateway, "model API"). With the
variable set, Claude Code doesn't fetch feature flags, so it reaches
`/api/eval/` only when `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is
unset, as in the second pass of the C06-model-api run.
The profile's allow of `/api/eval/` stays, because the variable isn't
the control. A new
Claude Code version needs a new image. Building an image for a new
Claude Code version must be cheap next to a full `wb image build`,
and how is open (I220). Plugins from the marketplace are unavailable
in the guest in V1, because `downloads.claude.ai` is outside the
profile.

### Ephemeral sessions

Ephemeral sessions use a throwaway guest user removed at session end
(FR14-ephemeral).

**Status:** Draft
