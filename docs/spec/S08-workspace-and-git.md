# S08 - Workspace and Git Round Trip

**Purpose:** How code gets into the guest and how the agent's work gets
back out, without sharing the host filesystem.

**Requirements:** FR02-any-repo to FR05-parallel-sessions, SEC02-no-host-fs-share, SEC03-no-host-exec, SEC10-audit, SEC13-bounded-resources, NFR01-startup, NFR02-fs-speed.

## Into the guest

- **Project clone.** On first use, the project user's home receives a
  clone of the host repository, transferred over vsock. Later sessions
  fetch only new objects.
- **Transport.** The guest uses a git remote helper (`git-remote-wb`,
  shipped with `wb-guestd`) for a remote named `host`. It tunnels git's
  smart protocol over vsock to `wb-hostd`, which runs `git upload-pack`
  **read-only** against the project's export repository
  (`projects/<id>/export.git`, B41-git-data-scope), never against the
  user's repository: hooks disabled, system and global git
  configuration ignored, environment scrubbed. Nothing in the guest can
  write to the host repository through this path.
- **Export repository.** `wb-hostd` fetches the session's branch from
  the user's repository into `export.git` at session start. It holds a
  copy of every object it serves and borrows from nothing. The guest can
  read everything in its object store: over protocol v2, `upload-pack`
  serves any object it has to a client that names the ID, so
  `uploadpack.hideRefs` on it only trims the ref list
  (X07-git-round-trip). The store also keeps objects from branches of
  earlier sessions until garbage collection removes them, and all of
  those are readable too. Its garbage collection and size cap are
  I41's item 6.
- **Protocol version.** Git doesn't tell a `connect` helper which
  protocol version it wants. The helper asks for v2 itself, and
  `wb-hostd` passes only `version=0`, `1` or `2` on to git.
- **Session start.** `wb-guestd` creates a worktree for the session at the
  host's current `HEAD`, then applies the carry-in: staged and unstaged
  changes as a binary diff, plus untracked files that are not ignored, up
  to a size cap (FR04-carry-in). Files over the cap are listed in the session
  summary rather than silently dropped.

## Out of the guest

- **Landing repository.** Each project has a bare repository in Wraith
  Box's state directory (`projects/<id>/landing.git`), not the user's
  repository. It borrows objects from the same project's `export.git`:
  at each session start `wb-hostd` rewrites its alternates file to
  exactly that one entry. A push then sends only new objects. Without
  borrowing, a session's first push sends the whole history
  (X07-git-round-trip). The guest pushes to it through the same
  transport; `wb-hostd` runs `git receive-pack` with hooks disabled,
  every object check (`fsck`) an error, including the checks git only
  reports by default, deletes, push options and signed pushes
  (`push-cert`) refused, and a size limit.
- **Ref restriction.** A filter in `wb-hostd` reads the push's command
  list before `receive-pack` sees any of it, and refuses the whole push
  unless every ref is under `refs/heads/wb/<session-id>/`. Below that
  prefix, ref components use only `A-Z`, `a-z`, `0-9`, `-`, `_` and
  `.`, don't start with `.` or end with `.` or `.lock`, and hold no
  `..`. A component is at most 255 bytes and a ref at most 1024.
  `shallow` lines are refused. The filter is a parser of guest bytes,
  so it has a fuzz target (S11-verification-and-spikes), and it logs
  each ref it allows or refuses with the rule. `receive.hideRefs`
  restricts the same refs inside git as a second layer, and hides other
  sessions' branches. It can't be the only layer: it accepts
  `refs/heads/wb/<session-id>` itself, and applies the allowed half of a
  mixed push (X07-git-round-trip).
- **Cleanup after a refused push.** Git moves a pushed pack out of
  quarantine before some of its own ref checks (a ref name git rejects,
  a stale lease, a directory/file conflict, a case clash on APFS). So
  after any push that didn't update every ref it named, and while no
  other push to it runs, `wb-hostd` drops the unreachable objects of
  `landing.git`: `git repack -a -d -l`, then
  `git prune --expire=now`. Pushes to one landing repository and its
  cleanup are serialized by a per-project lock in `wb-hostd`, because
  sessions share it (FR05-parallel-sessions) and a cleanup would
  otherwise prune a concurrent push's objects in the moment between
  leaving quarantine and its ref update. A size cap per project bounds
  `landing.git` (B41-git-data-scope, item 6).
- **Bounds.** Next to the size limit, which counts compressed bytes
  only (SEC13-bounded-resources): a deadline and an idle timeout per
  connection, at most one `upload-pack` per session at a time, at most
  one `receive-pack` per project at a time (the landing lock above), and
  a wall-clock watchdog that kills the git child. A cap on the inflated
  size of objects is an open point.
- **Session end.** If the worktree has uncommitted changes, `wb-guestd`
  commits them to the session branch as a clearly marked WIP commit, then
  pushes. `wb` can also push mid-session.
- **Landing on the host.** `wb land <session>` runs the user's own
  `git fetch` from the landing repository into `wb/<session-id>` in the
  user's repository. It never checks out, merges, or runs anything. A
  fetch only writes objects and refs.

## Flagging risky changes (SEC03-no-host-exec)

`wb diff` and the end-of-session summary flag changed paths that the
host or CI may execute or that change how git behaves:

- hook managers and hook directories;
- shell environment files (for example `.envrc`);
- build entry points: `Makefile`, task-runner files, `package.json`
  `scripts`, toolchain pin files;
- editor and agent configuration (`.vscode/`, `.idea/`, `.claude/`,
  `AGENTS.md`, `CLAUDE.md`);
- CI definitions (`.github/workflows/` and equivalents);
- `.gitattributes`, `.gitmodules`, submodule URL changes;
- lockfiles;
- new executables, executable-bit changes, and symlinks pointing outside
  the repository.

Git itself never transfers `.git/hooks` or repository configuration, so
those cannot arrive this way. The object checks on push refuse a tree
that tries (`.git`, `.GIT`, `git~1`), and a symlinked `.gitmodules` or
`.gitattributes`.

The flagger takes executables, symlinks and submodules from the git
file mode, and reads the content where the path alone doesn't say
enough: symlink targets, `package.json` `scripts` and `bin`, and the
`go` and `toolchain` lines of `go.mod`. The summary groups flags by
rule, because a project that often changes its CI or agent
configuration gets many (X07-git-round-trip).

## Other transfers

Files copied out of the guest by any means other than git (for example a
future `wb cp`) carry the platform's downloaded-file marker (the
quarantine attribute on macOS, Mark of the Web on Windows; S12-platforms).

## WSL

When `wb` runs inside WSL (S12-platforms), the repository is in the WSL
distribution. The WSL-side `wb` runs `upload-pack` there, with the same
restrictions, and tunnels it; `wb land` fetches from the landing
repository on the Windows side through the same channel. Everything
else in this spec is unchanged.

## Open points

Git LFS objects, submodules, and very large repositories are not covered
in v1. They are listed in V1-initial.

The inflated size of pushed objects has no cap yet. The candidate
mechanism is a `pre-receive` check that `wb-hostd` owns, run from a hooks
directory that `wb-hostd` creates, not one the repository supplies, so
hooks stay disabled for guest content. It would read object sizes in
the quarantine (`git cat-file --batch-check`), and repeat the checks git
only runs in `update()`: ref-name length, the old object ID,
directory/file and case conflicts. A failing `pre-receive` deletes the
quarantine before git moves it, so the cleanup after a refused push
would become a fallback. This is untested, and I74 is the spike for it.
If the check reads pack headers in Go instead, it is a new parser of
guest bytes for the fuzz list of S11-verification-and-spikes.

**Status:** Draft
