# S08-02 - Export Repository

**Purpose:** Which refs and objects the guest can fetch from `export.git`, and how
`wb`, its one writer, keeps it safe to run git in.

## Design

### Selected refs

`export.git` holds only the refs that some
live session of the project selected (B41-git-data-scope, item 1).
A session is live from its start until it ends. A lost session
(S06-vm-lifecycle, "Session lifecycle") stays live until its
recovery, `wb discard` or `wb project rm` ends it, so its WIP push
still has its base. A session that has
ended but isn't discarded yet isn't live: its pushed work in
`landing.git` is kept by the copy in S08-07-repository-lifecycle,
not by refs in `export.git`. A session selects the branch
the user's `HEAD` points to, plus the refs listed in `export_refs` in
the project's settings (`<config>/projects/<project-id>.toml`,
S04-architecture). An entry there is a full ref name, matched
exactly, and a listed ref the user's repository doesn't have is
skipped with a message. The list is host configuration only.
Repository-supplied configuration (`.wraithbox/`,
S09-policy-credentials-audit) can't add a ref, because the guest can
write it.

### Parallel sessions

Sessions of one project run in parallel (FR05-parallel-sessions), so
`export.git` holds the selections of every live session, not only the
newest. Each session's selection is kept under
`refs/wb/session/<session-id>/`, at the commits it had when the
session started: `refs/wb/session/<session-id>/HEAD` for the user's
`HEAD`, also when it is detached, and the full name of each other
selected ref below that prefix. The newest session's selection is
also under the plain names, with `export.git`'s `HEAD` pointing to
its branch, or detached at its commit. Every commit a live session
starts from is then reachable from a ref, which `receive-pack`
advertises to a push into `landing.git` as a base (below).

### Hidden refs

The guest's `upload-pack` runs with `uploadpack.hideRefs` set to
`refs/wb/session/` and `!refs/wb/session/<own-session-id>/`, so a
session's guest isn't offered other sessions' refs. That is cosmetic:
over protocol v2 the guest can still fetch any object in `export.git`
by ID, so a guest can read the bases of the project's other live
sessions, and keeps what it fetched earlier (T12-shared-export).

### One writer

`wb-hostd` never opens the user's repository, and `export.git` has
one writer, `wb` (I67). At session start, `wb`, running as the user,
takes the project's export lock through `wb-hostd`'s local IPC
(S08-07-repository-lifecycle) and runs one `git fetch` from the
user's repository into `export.git`, with an explicit source and
destination ref for each selected ref. That fetch runs
`git upload-pack` on the user's repository locally. `wb` then deletes
the refs of sessions that `wb-hostd` reports as no longer live, after
it checks each session ID against the session ID format. `wb-hostd`
only serves `export.git` to the guest, read-only, through the
`wb-git-upload` shim, whose profile doesn't allow writing it.
`wb-hostd`'s own profile doesn't allow writing `export.git`, the
project directory that holds it, or the projects root
(S04-architecture, "Each host daemon is self-sandboxed"), because
`wb` runs git in that repository and git trusts a repository's own
configuration. `wb` creates the project directory, and `export.git`
in it with `git init --bare --template=`, which doesn't copy
template files, and then writes `config` itself. That denial is the control.

### Layout check

As a second layer, before each run `wb` checks that `export.git`
holds only the fixed layout it creates and the files its own git
commands write: `HEAD`, `config`, `packed-refs`, refs under `refs/`,
loose objects under `objects/`, the directories `objects/info` and
`objects/pack`, and in `objects/pack` packs with their `.idx`,
`.rev` and `.bitmap` side files. Every entry must be a regular file
or a directory, and every file must have a link count of 1, so a
hard link into `export.git` is refused too. `config` must hold
exactly what `wb` wrote. Any other file refuses the run, among them
`objects/info/alternates`, `objects/info/packs`, `info/refs`,
`FETCH_HEAD`, `commondir`, `info/grafts` and `shallow`. So `wb`
runs every git command in `export.git` with
`core.hooksPath=/dev/null`, `gc.auto=0`, `maintenance.auto=false`
and `repack.updateServerInfo=false` on its command line, and fetches
with `--no-write-fetch-head`. While it holds the export lock, and
before the check, it removes what an interrupted run of its own
leaves behind, because no other process writes there: `HEAD.lock`,
`config.lock`, `packed-refs.lock`, `*.lock` files under `refs/`, and
`objects/pack/tmp_*` and `objects/pack/*.keep`. It examines each one
with `lstat`, and refuses the run instead of removing an entry that
isn't a regular file with a link count of 1.

### Partial clones

On a partial clone, `upload-pack` refuses to fetch missing objects
from the user's `origin` (`git-upload-pack(1)`, `GIT_NO_LAZY_FETCH`),
and `wb` sets `GIT_NO_LAZY_FETCH=1` as well. An update that needs
such an object fails, and the session doesn't start, with that
reason.

### What the guest can read

`export.git` holds a copy of every object it serves and borrows from
nothing. The guest can read everything in its object store. Over
protocol v2, `upload-pack` serves any object it has to a client that
names the ID, also one that `uploadpack.hideRefs` hides, so
`uploadpack.hideRefs` only trims the advertised ref list and isn't
access control (X07-git-round-trip). Objects reachable only from the
user's other branches, tags, notes and stash are never in
`export.git`, so a guest that knows such an object's ID still can't
fetch it (X07-git-round-trip). When a ref leaves the selection, its
objects are removed as S08-07-repository-lifecycle says.

**Status:** Draft
