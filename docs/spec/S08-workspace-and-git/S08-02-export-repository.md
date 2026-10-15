# S08-02 - Export Repository

**Purpose:** Which refs and objects the guest can fetch from
`export.git`, and how `wb`, its one writer, keeps it safe to run git in.

## Design

### Selected refs

`export.git` holds only the refs that some live session of the project
selected. A session selects:

- the branch the user's `HEAD` points to;
- the refs listed in `export_refs` in the project's settings,
  `<config>/projects/<project-id>.toml` (S04-architecture).

An entry in `export_refs` is a full ref name, matched exactly. A listed
ref the user's repository doesn't have is skipped with a message. The
list is host configuration only. Repository-supplied configuration
(`.wraithbox/`, S09-policy-credentials-audit) can't add a ref, because
the guest can write it.

A session is live from its start until it ends.

- A lost session (S06-vm-lifecycle, "Session lifecycle") stays live
  until its recovery, `wb discard` or `wb project rm` ends it. Its WIP
  push then still has its base.
- A session that has ended but isn't discarded yet isn't live. Its
  pushed work in `landing.git` is kept by the copy in
  S08-07-repository-lifecycle, not by refs in `export.git`.

### Parallel sessions

Sessions of one project run in parallel (FR05-parallel-sessions).
`export.git` holds the selections of every live session, not only the
newest.

- Each session's selection is kept under
  `refs/wb/session/<session-id>/`, at the commits it had when the
  session started.
- `refs/wb/session/<session-id>/HEAD` holds the user's `HEAD`, also
  when it is detached. Each other selected ref is below that prefix
  under its full name.
- The newest session's selection is also under the plain names.
  `export.git`'s `HEAD` points to its branch, or is detached at its
  commit.

Every commit a live session starts from is then reachable from a ref.
`receive-pack` advertises those refs to a push into `landing.git` as
bases (S08-07-repository-lifecycle, "Borrowed objects").

### Hidden refs

The guest's `upload-pack` runs with `uploadpack.hideRefs` set to
`refs/wb/session/` and `!refs/wb/session/<own-session-id>/`. A
session's guest isn't offered other sessions' refs.

That is cosmetic. Over protocol v2 the guest can still fetch any object
in `export.git` by ID. So a guest can read the bases of the project's
other live sessions, and keeps what it fetched earlier
(T12-shared-export).

### One writer

`wb-hostd` never opens the user's repository, and `export.git` has one
writer, `wb`. At session start, `wb` runs as the user and:

1. takes the project's export lock through `wb-hostd`'s local IPC
   (S08-07-repository-lifecycle);
2. runs one `git fetch` from the user's repository into `export.git`,
   with an explicit source and destination ref for each selected ref.
   That fetch runs `git upload-pack` on the user's repository locally;
3. deletes the refs of sessions that `wb-hostd` reports as no longer
   live, after it checks each session ID against the session ID
   format.

`wb-hostd` only serves `export.git` to the guest, read-only, through
the `wb-git-upload` shim. The shim's profile doesn't allow writing it.

`wb-hostd`'s own profile doesn't allow writing `export.git`, the
project directory that holds it, or the projects root
(S04-architecture, "Each host daemon is self-sandboxed"). That denial
is the control. `wb` runs git in that repository, and git trusts a
repository's own configuration.

`wb` creates the project directory, and `export.git` in it with
`git init --bare --template=`, which doesn't copy template files. Then
it writes `config` itself.

### Layout check

As a second layer, before each run `wb` checks that `export.git` holds
only the fixed layout it creates and the files its own git commands
write:

- `HEAD`, `config` and `packed-refs`;
- refs under `refs/`;
- loose objects under `objects/`;
- the directories `objects/info` and `objects/pack`;
- in `objects/pack`, packs with their `.idx`, `.rev` and `.bitmap` side
  files.

Every entry must be a regular file or a directory. Every file must
have a link count of 1, so a hard link into `export.git` is refused
too. `config` must hold exactly what `wb` wrote.

Any other file refuses the run. Among them are
`objects/info/alternates`, `objects/info/packs`, `info/refs`,
`FETCH_HEAD`, `commondir`, `info/grafts` and `shallow`. To keep its own
runs inside that layout, `wb` runs every git command in `export.git`
with these settings on its command line:

- `core.hooksPath=/dev/null`;
- `gc.auto=0` and `maintenance.auto=false`;
- `repack.updateServerInfo=false`.

It fetches with `--no-write-fetch-head`.

An interrupted run of `wb` can leave files behind. While it holds the
export lock, and before the check, `wb` removes them, because no other
process writes there:

- `HEAD.lock`, `config.lock` and `packed-refs.lock`;
- `*.lock` files under `refs/`;
- `objects/pack/tmp_*` and `objects/pack/*.keep`.

It examines each one with `lstat`. It refuses the run instead of
removing an entry that isn't a regular file with a link count of 1.

### Partial clones

On a partial clone, `upload-pack` refuses to fetch missing objects from
the user's `origin` (`git-upload-pack(1)`, `GIT_NO_LAZY_FETCH`). `wb`
sets `GIT_NO_LAZY_FETCH=1` as well. An update that needs such an object
fails, and the session doesn't start, with that reason.

### What the guest can read

`export.git` holds a copy of every object it serves, and borrows from
nothing. The guest can read everything in its object store.

- Over protocol v2, `upload-pack` serves any object it has to a client
  that names the ID, also one that `uploadpack.hideRefs` hides. So
  `uploadpack.hideRefs` only trims the advertised ref list, and isn't
  access control (X07-git-round-trip).
- Objects reachable only from the user's other branches, tags, notes
  and stash are never in `export.git`. A guest that knows such an
  object's ID still can't fetch it (X07-git-round-trip).

When a ref leaves the selection, its objects are removed as
S08-07-repository-lifecycle says.

## Decisions

### Only selected refs

`export.git` holds only the refs that live sessions selected, not the
whole user repository.

- **Why:** over protocol v2 the guest can fetch any object it can name.
  What isn't in `export.git` is the only thing it can't read.
- **Decided:** B41-git-data-scope, item 1.

### `wb` writes `export.git`, not `wb-hostd`

- **Why:** `wb-hostd` faces the guest and never opens the user's
  repository. Git trusts a repository's own configuration, so
  `wb-hostd` must not be able to write the repository `wb` runs git in.
- **Decided:** I67.

**Status:** Draft
