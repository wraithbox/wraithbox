# S08-06 - Landing on the Host

**Purpose:** How `wb land` and `wb diff` bring a session's work into
the user's repository, and the checks that keep that fetch from running
anything.

## Design

### Landing on the host

`wb land <session>` runs `git fetch` from the landing repository into
`wb/<session-id>` in the user's repository. It uses the git binary of
S08-08-host-git. It never checks out, merges, or runs anything. A
fetch only writes objects and refs.

`landing.git` is only ever the remote of a fetch. `wb` never runs git
with it as its repository, or with any other directory `wb-hostd` can
write (S04-architecture, "`wb` takes no path, URL or program from
`wb-hostd`").

### The user's repository

`wb land` and `wb diff` resolve the user's repository from the current
directory, by the rules `wb claude` uses (S05-cli, "Naming"). They
never take it from a path in `state.db` or from `wb-hostd`. They
refuse:

- a session that doesn't belong to that project;
- a project key under `<data>`, `<config>` or `<logs>`.

### The landing path

`wb` derives the landing repository's path itself, as
`<data>/projects/<project-id>/landing.git`. It checks the project ID
against the project ID format first. It compares that path with the
one `wb-hostd` reports, and refuses on a mismatch.

`wb` never passes git a path or URL that `wb-hostd` handed it. So a
compromised `wb-hostd` can't make it fetch from an `ext::` URL or
another repository (SEC12-least-privilege).

### Checks before the fetch

Before each fetch from `landing.git`, `wb` checks it, and refuses the
command when a check fails:

- With `lstat`, each of these is a directory and not a symbolic link:
  the projects root, the project directory, `landing.git`, `objects`,
  `objects/info` and `refs`.
- `objects/info/alternates` holds exactly one line,
  `<real path of the projects root>/<project-id>/export.git/objects`.
- There is no `commondir`, `gitdir`, `info/grafts`, `shallow` or
  `objects/info/http-alternates`.
- There is no ref under `refs/replace/`, loose or in `packed-refs`.

`wb-hostd` can write `landing.git`, so these checks only narrow what it
can do. It can swap the directory between the check and the fetch. It
can rewrite `objects/info/alternates` and the refs. And it can put
objects of any project in the session's branch (T16-hostd-landing).

### Object checks

The fetch checks every object it receives, with these settings on
git's command line:

- `fetch.fsckObjects=true` and `transfer.fsckObjects=true`;
- each check that `receive-pack` sets to an error, set to an error here
  too (`fetch.fsck.<msg-id>=error`).

A failed check refuses the land, and the fetch doesn't update a ref in
the user's repository. This repeats the checks of the push, for objects
that reached `landing.git` another way (SEC03-no-host-exec).

### Fetch settings

The fetch runs with `core.hooksPath=/dev/null`,
`maintenance.auto=false` and `gc.auto=0` on git's command line. Git
then doesn't run a hook or maintenance in the user's repository during
it.

Only the local transport is allowed:

- `GIT_PROTOCOL_FROM_USER=0` in its environment;
- `protocol.allow=never`, followed by `protocol.file.allow=always`, on
  its command line.

Every other transport is refused, `ext::`, `https`, `ssh` and `git`
among them. With `GIT_PROTOCOL_FROM_USER=0` and without
`protocol.file.allow`, git 2.54.0 and 2.56.0 refuse the local fetch too
(`transport 'file' not allowed`).

The fetch runs with `--no-recurse-submodules`.
`fetch.recurseSubmodules` defaults to `on-demand`, and
`protocol.file.allow=always` would then also apply to submodule
fetches of commits the guest chose.

### Protected configuration

The fetch starts `upload-pack` in `landing.git`, whose configuration
`wb-hostd` can write. That relies on git's rule for protected
configuration. `upload-pack` runs `uploadpack.packObjectsHook` only
when the setting comes from the system, global or command-line
configuration, never from the repository's own (`git-config(1)`,
"Protected configuration", and `git-upload-pack(1)`, "SECURITY").

### `wb diff`

`wb diff` reads the session's commits the same way, and writes nothing
in the user's repository.

1. It creates a scratch bare repository with `git init --bare
   --template=`. The repository is in a new directory, readable only by
   the user, in the user's temporary directory, outside `<data>`,
   `<config>` and `<logs>`.
2. It writes the scratch repository's `config` itself.
3. It writes an alternates entry to the user repository's objects, so
   the session's base isn't copied.
4. It runs the same checks and the same fetch into the scratch
   repository.
5. It runs `git --no-pager diff` and `git --no-pager log` there, with
   `--no-ext-diff`, `--no-textconv` and `core.hooksPath=/dev/null`.
   `git log` also gets `--no-show-signature`.
6. It removes the scratch repository when it is done.

Every git command in the scratch repository runs with
`GIT_NO_LAZY_FETCH=1`. A missing object then never makes git fetch it
from a promisor remote of the user's repository.

**Status:** Draft
