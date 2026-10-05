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
  user's repository: repository hooks disabled, system and global git
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
  transport. `wb-hostd` runs `git receive-pack` with these settings:
  - repository hooks disabled: `core.hooksPath` is the absolute path of
    a hooks directory in the read-only installed bundle, which holds
    only the pre-receive check below, and `receive.procReceiveRefs` is
    unset;
  - every object check (`fsck`) an error, including the checks git
    only reports by default;
  - deletes, push options and signed pushes (`push-cert`) refused;
  - `receive.unpackLimit=1`, so every push goes through `index-pack`.
    `unpack-objects --strict` keeps the trees and commits of a push in
    memory until it ends, which only the per-push cap would bound;
  - `pack.threads=1`;
  - a size limit (`receive.maxInputSize`).
- **Ref restriction.** A filter in `wb-hostd` reads the push's command
  list before `receive-pack` sees any of it, and refuses the whole push
  unless every ref is under `refs/heads/wb/<session-id>/`. Below that
  prefix, ref components use only `A-Z`, `a-z`, `0-9`, `-`, `_` and
  `.`, don't start with `.` or end with `.` or `.lock`, and hold no
  `..`. A component is at most 255 bytes and a ref at most 1024. Object
  IDs must have the length of the landing repository's hash. `shallow`
  lines are refused. The filter reads no byte past the flush that ends
  the command list, so the pack scanner gets the pack from its first
  byte. The filter is a parser of guest bytes, so it has a fuzz target
  (S11-verification-and-spikes), and it logs each ref it allows or
  refuses with the rule. `receive.hideRefs` restricts the same refs
  inside git as a second layer, and hides other sessions' branches. It
  can't be the only layer: it accepts `refs/heads/wb/<session-id>`
  itself, and applies the allowed half of a mixed push
  (X07-git-round-trip).
- **Pack scanner.** Git sizes its buffers from numbers in the pack: the
  object count in the pack header, the header size of each object, and
  the sizes at the start of each delta. It allocates them while it
  unpacks, before any hook runs, so an 8 KiB pack made `receive-pack`
  use 2 GiB of memory (X26-pre-receive-check). A scanner in `wb-hostd`
  reads the pack after the ref filter and before git does. It holds each
  entry until the entry has passed, then forwards it to `receive-pack`.
  It refuses the push when one of these caps is exceeded:
  - wire bytes: the whole pack over the size limit, or one entry over
    its bound, counted while the scanner reads. An entry's bound is its
    header, its OFS_DELTA offset or REF_DELTA base ID, and zlib's
    conservative bound for the size N it declares, about
    N + N/8 + N/64 + 11 bytes. That is the bound zlib gives for
    non-default parameters, not the tighter one for its defaults, which
    zlib-ng at level 1 and fixed-Huffman encoders can exceed. Zlib can
    consume input without producing output, so the caps on inflated
    sizes alone don't bound what the scanner holds;
  - per object (default 100 MiB): an object's inflated size, a delta's
    result size, a delta's declared source size, and a delta's own
    inflated data length. A delta whose base is in `export.git` is held
    to the same cap through its declared source size. A residual stays:
    a delta can declare the size of the largest object in the user's
    own repository, under the cap, and git loads that object once
    before the push fails closed. That is accepted, because the content
    is the user's own;
  - per push (default 1 GiB): the sum of all object and delta result
    sizes and delta data lengths. It counts resolved sizes, not new
    bytes, so 40 edits of a 30 MiB file exceed it in a few KiB. The cap
    is configurable per project, and the refusal names the cap and the
    total, and says to push fewer commits at a time or raise the cap;
  - object count (default 1,000,000): the count in the pack header,
    checked before the scanner forwards the header. `index-pack`
    allocates its object table from that count, and the scanner keeps
    one size per entry.

  An entry's size header is at most 9 bytes with a value under 2^60,
  as `unpack_object_header_buffer` reads it in git 2.56.0 on 64-bit. A
  delta's size fields are at most 10 bytes each, with values that fit in
  64 bits. A longer field, or one with bits past those limits, is
  malformed and refused. The scanner inflates
  each entry only to find the next one, and stops one byte past the
  declared size. After the pack's checksum it forwards nothing more and
  closes git's input. A push with an empty command list carries no pack,
  and the scanner doesn't run. The scanner is a parser of guest bytes, so
  it has a fuzz target (S11-verification-and-spikes). It logs each
  refusal with the rule, and each push it passes with its object count,
  wire bytes and total size.
- **Pre-receive check.** Git moves a pushed pack out of quarantine
  before some of its own ref checks, which it makes only in `update()`
  (X07-git-round-trip). A small `pre-receive` program, separate from
  `wb-hostd` and shipped in the hooks directory, makes them first, while
  the objects are still in quarantine. When it refuses, git deletes the
  quarantine and refuses every ref of the push, so nothing of it
  reaches `landing.git` (X26-pre-receive-check). It refuses the push
  when, for any ref:
  - the ref's lock file path (`<landing.git>/<ref>.lock`) is longer
    than the file system allows, or a component plus `.lock` is longer
    than 255 bytes;
  - the old object ID isn't the ref's current value (for a create, the
    ref must not exist);
  - another ref, existing (loose or packed) or in the same push, is a
    directory prefix of it or has it as one;
  - another ref, existing or in the same push, has a path prefix equal
    to one of its own when case is folded but not byte for byte, which
    would be the same file on APFS or NTFS;
  - an object in the quarantine is over the per-object cap, or all of
    the objects together are over the per-push cap. It lists only the
    quarantine (`git cat-file --batch-all-objects` with
    `GIT_OBJECT_DIRECTORY` set to it and no alternates). The bases that
    `index-pack --fix-thin` copies in from `export.git` are in the
    quarantine, so they count.

  It runs under the landing lock below. The refs it reads are then the
  refs `update()` sees. Its limits come from the environment `wb-hostd`
  sets on `receive-pack`, and a missing limit refuses everything. It
  writes each decision with its rule to a descriptor that `wb-hostd`
  opens and `receive-pack` passes on. It takes the descriptor's number
  from an environment variable `wb-hostd` sets, and a missing variable
  or descriptor is a refusal. Its standard error goes to the guest, so it writes only the
  rule name there. For the object caps it adds a second layer to the
  scanner, and for the ref checks it is the only one. At start,
  `wb-hostd` checks that the hooks directory holds exactly this one
  file, and refuses pushes when it doesn't.
- **Cleanup after a push that didn't end all `ok`.** Some late failures
  get past the pre-receive check. A lock file left by a crashed push, or
  an I/O error, still fails a ref in `update()` after git moved the pack
  (X26-pre-receive-check). A `receive-pack` that is killed, by the
  watchdog, a disconnect or a scanner refusal, can leave an
  `objects/tmp_objdir-*` quarantine and `*.lock` files that repack and
  prune don't remove. `wb-hostd` stops git with SIGTERM, which lets git
  remove its quarantine, and with SIGKILL after a grace period. So after
  every push in which not every ref reported `ok`, including kills,
  disconnects and scanner refusals, and while no other push to it runs,
  `wb-hostd` cleans `landing.git`: it removes `objects/tmp_objdir-*`,
  `*.lock` files under `refs/heads/wb/` and `packed-refs.lock` at
  the repository's root, then runs
  `git repack -a -d -l` and `git prune --expire=now`. Pushes to one
  landing repository and its cleanup are serialized by a per-project
  lock in `wb-hostd`, because sessions share it (FR05-parallel-sessions)
  and a cleanup would otherwise prune a concurrent push's objects in the
  moment between leaving quarantine and its ref update.
- **Landing size cap.** A successful push can also leave objects no ref
  reaches: extra objects in its pack, and the bases that
  `index-pack --fix-thin` copies in from `export.git`. A size cap per
  project bounds `landing.git`, those included (B41-git-data-scope,
  item 6). When a push would pass the cap, `wb-hostd` runs the same
  repack and prune first. When `landing.git` is still over the cap, the
  push is refused with a message that names the cap and says to land or
  discard finished sessions. `wb land` retries a fetch that fails while
  a repack runs.
- **Bounds.** Next to the size limit, which counts compressed bytes
  only (SEC13-bounded-resources): a deadline and an idle timeout per
  connection, at most one `upload-pack` per session at a time, at most
  one `receive-pack` per project at a time (the landing lock above), and
  a wall-clock watchdog that stops the git child. The pack scanner's
  caps bound the memory and CPU time git spends unpacking. With
  `receive.unpackLimit=1` and `pack.threads=1`, `index-pack` holds its
  object table, its delta base cache (`core.deltaBaseCacheLimit`), and
  a base and a result at a time. The peak measured under a 100 MiB cap
  was 207 MiB (X26-pre-receive-check). It is a measurement, and the
  conformance suite checks it again (S11-verification-and-spikes).
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
- Wraith Box's own repository configuration, `.wraithbox/`
  (S09-policy-credentials-audit);
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

**Status:** Draft
