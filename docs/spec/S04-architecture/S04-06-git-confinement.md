# S04-06 - Git and the `wb-hostd` Profile

**Purpose:** How git runs on data from the guest, what the `wb-hostd` profile
lets it write, and what `wb` takes from `wb-hostd`.

## Design

### `wb-git`

git, which the git gateway runs on data the guest sends, starts
through `wb-launcher` as `wb-git`, a shim that applies a profile for
one repository and then runs git (decided in B34-sandboxed-daemons,
not yet built). The launcher table
has two fixed entries, `wb-git-receive` (`git receive-pack`) and
`wb-git-upload` (`git upload-pack`), so the subcommand is never a
free string. Besides its program, a `wb-git` request holds one
field, the repository. `wb-launcher` takes the real path of the
projects root (`<data>/projects`, from configuration `wb-hostd` read
before it confined itself) once, when it starts. It accepts the
repository only if it equals, after cleaning,
`<real path of the projects root>/<id>/<basename>`, with `<id>` in
the project ID format and `<basename>` `landing.git` for
`wb-git-receive` and `export.git` for `wb-git-upload` (I41, I67).
It never resolves the request string itself,
which would follow a symbolic link an attacker placed. The shim
repeats the same check before it calls `sandbox_init`. The
repository becomes the shim's profile parameter and git's one path
argument. This is a bounded exception to "no arguments". The
Seatbelt match on the resolved path is the control that matters,
and the path check is defense in depth (X23-sandboxed-daemons).
Both profiles allow reading the git installation and writing
`/dev/null`. The only programs they let the shim start are git and
the programs of its own installation (`libexec/git-core/`), plus,
for `wb-git-receive`, the pre-receive check in the bundle's hooks
directory (S08-04-push-checks, "Pre-receive check"). Neither
allows a shell. A command that git would run through one, such as
a `core.alternateRefsCommand` from a repository's own
configuration, fails. `wb-hostd` writes `landing.git/config` as fixed content
when it creates the repository, checks at start that each
`landing.git/config` still holds exactly that content, and refuses
pushes to a project whose `config` differs. The `wb-git-upload` profile allows reading its
`export.git` and no other write, because `upload-pack` only reads
the repository it serves (I67). The `wb-git-receive` profile
allows reading and writing its `landing.git`, and reading, never
writing, the `export.git` next to it, which `landing.git` borrows
objects from (S08-03-pushes, "Landing repository").
Instead of a path, `wb-hostd` could hand over an inherited
directory descriptor that the launcher checks with `F_GETPATH`,
which keeps the path out of the request. That is the fallback if
the path check turns out to be fragile.

### The weaker fallback

The measured, weaker fallback is git as a child of `wb-hostd`,
under the `wb-hostd` profile. A git bug would then reach every
project's policy, the approvals in `state.db`, other projects'
landing repositories, and the audit log. The maintainer chose the
`wb-git` shim on I34. Falling back would need a spec change and a
T00-index entry for the residual risk.

### `export.git` has one writer

`wb-hostd` never opens the user's repository, and its profile
doesn't name one. `wb`, running as the user outside any profile,
fetches the selected refs from the user's repository into the
project's `export.git` and is its only writer. `wb-hostd` only
serves `export.git` to the guest, through `wb-git-upload`
(S08-02-export-repository, decided on I67).

### The `wb-hostd` profile

`wb-hostd`'s profile allows reading `<config>`, and reading and
writing `<logs>` and `<data>`, with these exceptions
(SEC12-least-privilege, SEC03-no-host-exec):
- Under the projects root `<data>/projects` it may read
  everything, and write only inside
  `<data>/projects/<id>/landing.git`: that path and everything
  below it, for any `<id>`. It can't write `export.git`, a project
  directory, or the projects root itself, so it can't create,
  rename or remove any of them.
- It can't write `<data>` itself, or any other directory between
  `<data>` and the projects root, so it can't rename or remove
  them. Seatbelt checks a rename against the two paths it names,
  not against what the moved directory holds. Without this rule
  `wb-hostd` could move `<data>` into `<logs>`, write the
  `export.git` inside it and move it back.
- It can't make a hard link whose source is under the projects root
  outside a `landing.git`. Seatbelt checks `file-link` against the
  source path as well as the new name, so the rule that denies
  writing `export.git` denies linking its files into a place
  `wb-hostd` may write.

`wb` runs git in `export.git`, and git trusts a repository's own
configuration, so a compromised `wb-hostd` that could write there
could make `wb` run a program. It can still rename `landing.git`
out of its project directory, replace it, or create it as a
symbolic link to anything, because the rule allows every write at
that path. So `wb` never runs git with `landing.git` as its
repository, and checks it before every fetch from it
(S08-06-landing-on-the-host).

On macOS the profile allows writes under `<data>` and `<logs>`,
then denies writes to the literal path of `<data>` and of each
directory between it and the projects root, then denies writes
under the projects root, then allows them again where both
`(subpath <projects root>)` and a regex match. The regex doesn't
hold any byte of the projects root's path, which therefore doesn't
need escaping. It pins the depth instead: `^` followed by one `/[^/]+` per
component of the projects root's real path, then
`/[^/]+/landing[.]git(/|$)`. `wb-hostd` builds it from that count
before it confines itself and passes it as a profile parameter. A
later Seatbelt rule takes precedence over an earlier one. So
`wb setup` creates the projects root before it starts the
service, `wb` creates each project directory and its `export.git`,
`wb-hostd` creates `landing.git` inside it, and at `wb project rm`
`wb` removes the project directory (S05-cli).

`wb setup` also creates a probe directory,
`<projects root>/.wb-probe/export.git`, with one file in it,
`probe`. `.wb-probe` isn't in the project ID format, so no request
can name it. These probes check the macOS profile. They don't
carry over to Windows as they are, because there `<logs>` is
inside `<data>` (S12-platforms, "Paths"). I190 covers Linux and
Windows. Right after it confines itself, `wb-hostd`
tries each of these, and exits when one succeeds or fails for any
reason other than the sandbox's denial. When the probe file is
missing, it exits with a message that says to run `wb setup`,
which creates it again (NFR06-explained-refusals):
- creating a file in the projects root;
- writing `.wb-probe/export.git/probe`;
- hard-linking `.wb-probe/export.git/probe` to a fixed name in
  `<data>/run`;
- renaming `<data>` to a fixed name in `<logs>`;
- renaming `.wb-probe` to a fixed name in `<data>/run`;
- creating `.wb-probe/landing.git-probe`, next to where a
  `landing.git` would be, which checks that the regex ends at
  `landing.git` or a `/` after it.

### `wb` takes no path, URL or program from `wb-hostd`

`wb` runs
as the user outside any profile, so what it runs is the boundary
that SEC12-least-privilege puts around a compromised `wb-hostd`. It
never runs a binary that `wb-hostd` names, never passes git a path
or URL that `wb-hostd` handed it, and never runs git with a
repository that `wb-hostd` can write as its repository. Git reads
such a repository for `wb` in two places only. One is the
`upload-pack` that a fetch from it starts. The other is, on WSL, the
`git upload-pack` that `wb.exe landing-upload-pack` runs on
`landing.git` after its checks, for the fetch of the WSL-side `wb`
(S08-10-wsl). Neither runs a command from that
repository's own configuration (protected configuration,
S08-06-landing-on-the-host). It derives
each path under `<data>` itself, from IDs it checks against their
format, and compares any path `wb-hostd` reports with its own,
refusing on a mismatch. A directory `wb-hostd` can write, such as
`landing.git`, is only ever the remote of a fetch that `wb` runs in
another repository (S08-06-landing-on-the-host, S08-08-host-git).

**Status:** Draft
