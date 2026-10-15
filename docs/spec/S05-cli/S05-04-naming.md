# S05-04 - Naming

**Purpose:** How `wb` identifies a project, a session and its branch, and how
projects are registered, checked, moved and removed.

## Design

### Project key

the host realm and the absolute path of the
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

### Resolving the key

`wb` runs git for the key, the remotes and the
root commits with the git binary of S08-08-host-git, `core.hooksPath=/dev/null` on its command line, and an
otherwise scrubbed environment, as `wb-git` runs git
(S08-03-pushes). It sets only `HOME`,
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
  project's (S08-06-landing-on-the-host).
- *Wraith Box's own directories.* A key under `<data>`, `<config>`
  or `<logs>` refuses the command, so no command treats a repository
  that `wb-hostd` can write, such as a `landing.git`, as the user's.

### Project id

128 random bits at registration (FR02-any-repo), written
as 32 lowercase hexadecimal characters (the project ID format), kept
for the project's life. The id isn't derived from the key, so a new
repository at a moved project's old path never gets its id. The
`projects` table of `state.db` (S04-architecture, "Host state") maps
each key to one id, and a key has at most one project.

### Recorded identity

at registration `wb` records every URL of each
remote as the user's git resolves it, after the cleaning of
S08-01-into-the-guest ("Remote URLs"). An SSH remote is compared as
its `https://host/path` form, without user, port, password, query or
fragment, so a switch between SSH and HTTPS for the same repository,
or a rotated token, isn't a difference. A URL the guest doesn't get
is compared with its user name, password, query string and fragment
removed. For a repository with no remote, `wb` also records its root
commits (`git rev-list --max-parents=0 --all`). It doesn't record
them for a repository with a remote, because the walk reads the whole
history at every session start, and the URLs already tell repositories
apart.

### Grow-only

at a session start where the recorded identity is empty
and the repository now has a remote or a root commit, `wb` records
it, with a 5019 event. A repository that has had neither a remote
nor a commit at any session start has nothing recorded, so a
replacement at its path before then is the same project.

### Identity check

Checked at each session start, and by every other command that
resolves the project from a directory except `wb project confirm`
and `wb project rm`: `wb` compares the recorded identity
with the repository's current one. A difference in the URLs, or a
recorded root commit that is no longer among the current root
commits, refuses the command, and the message names what was
recorded, what is there now and `wb project confirm`
(NFR06-explained-refusals). Without this check a different repository
cloned into the same directory would inherit the old project's
approvals and policy.

### Confirming

`wb project confirm` records the current identity and clears
`config_trust`, so a replaced repository's `.wraithbox/` isn't read
until the user runs `wb trust` and its boundary check again
(SEC09-host-policy). It prints the `placement` and the approvals the
project keeps, and its 5019 event holds both URL and root commit
sets.

### Moving

`wb project move <dir>` resolves the key of `<dir>` by the
rules above, then changes the project's key to it and keeps the id. The
project's Claude Code state (FR13-claude-state), approvals, policy
and landing repository stay with it. It runs the identity comparison
for `<dir>` and prints any difference, and the next session start
then refuses until `wb project confirm`. A move to a key that another
project has is refused, with that project named.

### Registration

the first command that registers an unknown key (a
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

### Removing

`wb project rm` removes the project: its settings, policy and the
credential bindings in it, approvals, cached join approvals and the
learn list that name it, `export.git` and `landing.git`, and its row
in `state.db`. `wb` removes the project directory
`<data>/projects/<project-id>` with both repositories itself, because
`wb-hostd` can't remove it (S04-03-self-sandboxing, "Each host daemon is self-sandboxed"). The secret store items behind its bindings stay,
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
recovery push (S06-11-reconnect-and-recovery, "Discarding a lost session"). Branches
already landed in the user's repository stay. When a project holds a key or identity
that another project should have, the user removes it with
`wb project rm` and then moves the other one. `wb project move`
never replaces a project, so dropping a project's state and
approvals always takes this one explicit command.

### Name

a human-readable name is derived from the directory that holds
the common directory, and `wb project rename` changes it.

- The maintainer decided project identity on I42
  (B42-trust-placement).
- Session id: `<project-name>-<yyyymmdd>-<hhmm>-<4 random chars>`.
- Returned work lands on host branches `wb/<session-id>` by default.

**Status:** Draft
