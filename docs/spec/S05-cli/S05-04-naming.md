# S05-04 - Naming

**Purpose:** How `wb` identifies a project, a session and its branch, and how
projects are registered, checked, moved and removed.

## Design

### Project key

A project's key is the host realm, and the absolute path of the
repository's git common directory
(`git rev-parse --path-format=absolute --git-common-dir`).

- Every `git worktree` of one repository has the same common directory.
  So they are one project.
- A submodule has a common directory of its own, under the parent
  repository's `modules/`. So it is a project of its own.
- The remote URLs aren't part of the key. So a changed or added remote,
  or a token in a URL, doesn't make a new project.

The parts of the key:

- *Realm.* The native host, or a WSL distribution by name. A repository
  on a Windows drive, opened from WSL through `/mnt/c`, and the same
  repository opened from native Windows are in two realms. So they are
  two projects.
- *Path.* The key uses the path as git returns it, and `wb` doesn't
  recompute it. On macOS, git's absolute common directory already
  resolves symbolic links. It also folds letter case to the spelling on
  disk (APFS). So `~/Git/foo` and `~/git/foo` give one key. On other
  platforms, the canonical form comes from `internal/platform`
  (S12-platforms).

### Resolving the key

`wb` runs git for the key, the remotes and the root commits:

- with the git binary of S08-08-host-git;
- with `core.hooksPath=/dev/null` on its command line;
- with an otherwise scrubbed environment, as `wb-git` runs git
  (S08-03-pushes).

It sets only these variables:

- `HOME`, taken from the user database (the account's home directory),
  not from the environment;
- `XDG_CONFIG_HOME` from the login environment, when it is set there
  and is an absolute path;
- `TMPDIR`, the caller's temporary directory, which must be an
  absolute path;
- `LC_ALL=C`.

So no `GIT_*` variable, from the user's shell or a `.envrc`, can point
the directory at another repository or add configuration.

The login environment is the environment of the user's login session.
A variable exported in a shell, or set by a `.envrc`, doesn't reach it. On macOS, that is
launchd's: `wb` reads it with `/bin/launchctl getenv XDG_CONFIG_HOME`,
run with an empty environment. Linux and Windows hosts come later
(S12-platforms).

When a variable can't be worked out, the command is refused, and the
value is never taken from the caller's environment instead. That
happens on a failed user database or `launchctl` lookup, and on a
temporary directory that isn't an absolute path.

Unlike `wb-git`, it reads the user's global git configuration. So remote
URLs resolve as the user's git resolves them (`url.<base>.insteadOf`).

#### Which directory

`wb` walks up from the current directory to the first directory that
holds a `.git` entry, as git does. The key must belong to that
directory.

#### A `.git` file

That entry can be a file (`gitdir: …`) instead of a directory. `wb`
accepts it only for a linked worktree or a submodule:

- A linked worktree has `--git-dir` equal to
  `<common>/worktrees/<name>`. That worktree's back-link,
  `<common>/worktrees/<name>/gitdir`, names this `.git` file.
- A submodule has `--git-dir` equal to the common directory. Its
  `core.worktree`, resolved relative to `--git-dir`, canonicalizes to
  the directory that holds this `.git` file.

`wb` doesn't use `--show-toplevel` for this, because git derives it
from that same `core.worktree`.

Anything else refuses the command. An example is a `.git` file in a
downloaded archive that names another project's repository, or one of
its submodules.

#### Failure

Any failed `rev-parse` refuses the command, "dubious ownership"
included. Every refusal here says why (NFR06-explained-refusals), and
nothing is registered.

#### Which commands

Every command that resolves a project from a directory resolves it this
way:

- `wb claude`, `wb trust` and `wb untrust`;
- the `wb project` commands without `--project`;
- `wb project move` for its `<dir>`, also when `--project` is given;
- `wb land` and `wb diff`.

`wb land` and `wb diff` find the user's repository only this way, never
from a path in `state.db` or from `wb-hostd`. They refuse a session
that isn't the resolved project's (S08-06-landing-on-the-host).

#### Wraith Box's own directories

A key under `<data>`, `<config>` or `<logs>` refuses the command. So no
command treats a repository that `wb-hostd` can write, such as a
`landing.git`, as the user's.

### Project id

A project's id is 128 random bits, chosen at registration
(FR02-any-repo). It is written as 32 lowercase hexadecimal characters,
the project ID format. It is kept for the project's life.

The id isn't derived from the key. So a new repository at a moved
project's old path never gets its id.

The `projects` table of `state.db` (S04-architecture, "Host state") maps
each key to one id. A key has at most one project.

### Recorded identity

At registration, `wb` records every URL of each remote, as the user's
git resolves it. It does that after the cleaning of
S08-01-into-the-guest ("Remote URLs").

- An SSH remote is compared as its `https://host/path` form, without
  user, port, password, query or fragment. So a switch between SSH and
  HTTPS for the same repository isn't a difference, and neither is a
  rotated token.
- A URL the guest doesn't get is compared with its user name, password,
  query string and fragment removed.

For a repository with no remote, `wb` also records its root commits
(`git rev-list --max-parents=0 --all`). It doesn't record them for a
repository with a remote. The walk reads the whole history at every
session start, and the URLs already tell repositories apart.

### Grow-only

At a session start, the recorded identity can be empty while the
repository now has a remote or a root commit. `wb` then records it, with
a 5019 event.

A repository that has had neither a remote nor a commit at any session
start has nothing recorded. So a replacement at its path before then is
the same project.

### Identity check

`wb` compares the recorded identity with the repository's current one:

- at each session start;
- in every other command that resolves the project from a directory,
  except `wb project confirm` and `wb project rm`.

These refuse the command:

- a difference in the URLs;
- a recorded root commit that is no longer among the current root
  commits.

The message names what was recorded, what is there now, and
`wb project confirm` (NFR06-explained-refusals).

Without this check, a different repository cloned into the same
directory would inherit the old project's approvals and policy.

### Confirming

`wb project confirm` records the current identity, and clears
`config_trust`. So a replaced repository's `.wraithbox/` isn't read
until the user runs `wb trust` and its boundary check again
(SEC09-host-policy).

It prints the `placement`, and the approvals the project keeps. Its 5019
event holds both the URL set and the root commit set.

### Moving

`wb project move <dir>` resolves the key of `<dir>` by the rules above.
Then it changes the project's key to it, and keeps the id. The
project's Claude Code state (FR13-claude-state), approvals, policy and
landing repository stay with it.

It runs the identity comparison for `<dir>`, and prints any difference.
The next session start then refuses until `wb project confirm`.

A move to a key that another project has is refused, with that project
named.

### Registration

The first command that registers an unknown key registers a new
project. That command is a session start, or `wb project place`. The
rules are the same for both.

`wb` first compares the repository's identity with every existing
project's. A match is the same URLs, or, for a repository without a
remote, a shared root commit.

- *Moved.* It matches a project whose location no longer resolves to
  its key. `wb` refuses the command, and names that project with
  `wb project move`, which keeps its state, and `wb project rm`, which
  drops it.
- *Second clone.* Otherwise, a match is a second clone of a repository
  whose first clone is still in place. It registers as its own project.
  The registration notice names each matching project and its
  `placement` (NFR06-explained-refusals).

When any matching project has `placement = isolated`, the new project
starts with `placement = isolated` too. So a clone of a repository the
user isolated doesn't land in the work VM (T13-new-project-grants).
Only the placement is copied, never approvals, bindings, policy or
`config_trust`.

### Removing

`wb project rm` removes the project:

- its settings, its policy and the credential bindings in it;
- the approvals, cached join approvals and learn list that name it;
- `export.git` and `landing.git`;
- its row in `state.db`.

`wb` removes the project directory `<data>/projects/<project-id>`, with
both repositories, itself. `wb-hostd` can't remove it
(S04-03-self-sandboxing, "Each host daemon is self-sandboxed").

The secret store items behind its bindings stay. `wb cred` manages
them, and another project may use the same item. `rm` lists them, and
`wb cred rm` removes them.

#### The project user

`rm` asks `wb-guestd` in each VM to remove the project user and its
home.

- The pending removal is a record in `state.db` that survives restarts.
- `wb-hostd` sends it when the VM runs, and otherwise at its next start.

`wb-guestd`'s reply is untrusted (SEC11-root-gains-nothing):

- The record stays until a reply for that project comes.
- A reply never clears another project's record.
- A false reply can only leave the home inside that guest.

The host-side state is gone before the request is sent. Ids are random,
so no later project gets this one's id.

#### When `rm` is refused

`rm` is refused while the project has a session, or returned work that
wasn't landed or discarded. The refusal lists them.

A lost session doesn't count as a session here. `rm` ends it with no
recovery push (S06-11-reconnect-and-recovery, "Discarding a lost session").

Branches already landed in the user's repository stay.

#### Replacing a project

A project can hold a key or identity that another project should have.
The user then removes it with `wb project rm`, and moves the other one.
`wb project move` never replaces a project. So dropping a project's
state and approvals always takes this one explicit command.

### Name

A human-readable name is derived from the directory that holds the
common directory. `wb project rename` changes it.

### Session id and branch

- Session id: `<project-name>-<yyyymmdd>-<hhmm>-<4 random chars>`.
- Returned work lands on host branches `wb/<session-id>` by default.

## Decisions

### Project identity

The project key, the recorded identity and the rules for registration,
moving and confirming above.

- **Decided:** I42 (B42-trust-placement).

**Status:** Draft
