# S08 - Workspace and Git Round Trip

**Purpose:** How code gets into the guest and how the agent's work gets
back out, without sharing the host filesystem.

**Requirements:** FR02-any-repo to FR05-parallel-sessions, SEC02-no-host-fs-share, SEC03-no-host-exec, SEC04-no-guest-secrets, SEC06-repo-writes, SEC10-audit, SEC13-bounded-resources, NFR01-startup, NFR02-fs-speed.

## Into the guest

- **Project clone.** On first use, the project user's home receives a
  clone of the host repository, transferred over vsock. Later sessions
  fetch only new objects.
- **Transport.** The guest uses a git remote helper (`git-remote-wb`,
  shipped with `wb-guestd`) for a remote named `host`. It tunnels git's
  smart protocol over a local Unix socket to `wb-guestd`, which sends
  it to `wb-hostd` on a host-guest connection the host opened
  (S04-architecture, B29-vsock-handoff). `wb-hostd` runs `git upload-pack`
  **read-only** against the project's export repository
  (`projects/<id>/export.git`, B41-git-data-scope), never against the
  user's repository: repository hooks disabled, system and global git
  configuration ignored, environment scrubbed. Nothing in the guest can
  write to the host repository through this path.
- **Export repository.** `export.git` holds only the refs the guest
  may fetch (B41-git-data-scope, item 1): the branch the user's `HEAD`
  points to, as `export.git`'s `HEAD`, plus the refs listed in
  `export_refs` in the project's settings
  (`<config>/projects/<project-id>.toml`, S04-architecture). An entry
  there is a full ref name, matched exactly, and a listed ref the user's
  repository doesn't have is skipped with a message. On a detached
  `HEAD`, `export.git`'s `HEAD` is detached at the same commit. The list
  is host configuration only. Repository-supplied configuration
  (`.wraithbox/`, S09-policy-credentials-audit) can't add a ref, because
  the guest can write it. At session start the update makes
  `export.git`'s refs exactly that set: it fetches new objects and
  deletes refs that are no longer selected. `wb-hostd` never opens the
  user's repository. `wb`, running as the user, runs `git upload-pack`
  and the fetch into `export.git` itself and tunnels the stream to
  `wb-hostd`, as for WSL below (I67). The `upload-pack` on the user's
  repository sets `uploadpack.hideRefs` so that it advertises only the
  selected refs. The update holds the project's export lock
  ("Repository lifecycle" below).

  `export.git` holds a copy of every object it serves and borrows from
  nothing. The guest can read everything in its object store: over
  protocol v2, `upload-pack` serves any object it has to a client that
  names the ID, so `uploadpack.hideRefs` on it only trims the ref list
  (X07-git-round-trip). The store also keeps objects from branches of
  earlier sessions until garbage collection removes them, and all of
  those are readable too. Objects reachable only from the user's other
  branches, tags, notes and stash are never in it, so a guest that
  knows such an object's ID still can't fetch it (X07-git-round-trip).
- **Remote URLs** (B41-git-data-scope, item 3). The guest's clone gets
  the host repository's remotes next to `host`, so the agent can push
  to the forge through the egress gateway (SEC06-repo-writes). `wb`
  reads each URL as the user's git resolves it (`git remote get-url`,
  which applies `url.<base>.insteadOf`) and cleans it before it sends
  it on:
  - user name, password, query string and fragment are removed from
    every URL;
  - an SSH remote, `ssh://[user@]host[:port]/path` or the short form
    `[user@]host:path`, becomes `https://host/path` without the port,
    because the guest has no SSH keys (SEC04-no-guest-secrets) and
    reaches the forge only over HTTPS (I47);
  - an `https://` URL is kept, cleaned as above;
  - any other URL (`http://`, `git://`, `file://`, a local path, or a
    `<helper>::` URL) isn't given to the guest, and the session summary
    lists the remote with the reason.

  The guest doesn't get a `pushurl` and pushes to the cleaned URL. A
  `.gitmodules` URL is tracked content and isn't rewritten (submodules
  are in "Open points"). Project identity, which also reads a remote
  URL, is I42's (S05-cli, "Naming").
- **Protocol version.** Git doesn't tell a `connect` helper which
  protocol version it wants. The helper asks for v2 itself, and
  `wb-hostd` passes only `version=0`, `1` or `2` on to git.
- **Session start.** `wb-guestd` creates a worktree for the session at the
  host's current `HEAD`, then applies the carry-in: staged and unstaged
  changes as a binary diff, plus untracked files that are not ignored, up
  to a size cap (FR04-carry-in). Files over the cap are listed in the session
  summary rather than silently dropped.
- **Carry-in scan** (B41-git-data-scope, item 2). A new
  `credentials.json` that the user hasn't ignored yet is an untracked
  file that isn't ignored, so the carry-in would send it. `wb` collects
  the carry-in in the user's repository and scans each untracked file,
  and each file's part of the staged and unstaged diff, with the secret
  scanner of the image seal (S06-vm-lifecycle, "Sealing") before it
  sends any of it on (SEC04-no-guest-secrets). A file with a match is
  skipped whole: the guest doesn't get an untracked file, and a tracked
  file keeps its `HEAD` version in the guest. The session summary lists each
  skipped file with the scanner rule that matched, never the matched
  text, as for oversize files, and `wb` logs the decision with the
  rule. When the scanner fails or times out, no carry-in reaches the
  guest: the session starts at `HEAD` without it and says why.
  Committed content isn't scanned, because committing a file on the
  selected branch is the user's choice to send it.

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
  - `receive.unpackLimit=1`, so every push goes through `index-pack`,
    for two reasons. `unpack-objects --strict` keeps the trees and
    commits of a push in memory until it ends, which only the per-push
    cap would bound. And `unpack-objects` resolves a delta as soon as
    its base has arrived, while `index-pack` waits for the pack's
    checksum, which the pack scanner below holds back
    (X28-git-alloc-limit);
  - `pack.threads=1`;
  - a size limit, `receive.maxInputSize`, of 1 GiB, the same value as
    the scanner's wire-byte cap below;
  - `GIT_ALLOC_LIMIT` in its environment, at the per-object cap plus
    one byte (104857601), which its children inherit. Git then refuses
    each allocation above it before making it
    (X28-git-alloc-limit, "Pack scanner" below). The environment
    builder in `wb-hostd` has a unit test that pins this value
    (S11-verification-and-spikes). Git reads `0` as no limit, so the
    value is never computed from a setting that can be zero.
- **Ref restriction.** A filter in `wb-hostd` reads the push's command
  list before `receive-pack` sees any of it, and refuses the whole push
  unless every ref is under `refs/heads/wb/<session-id>/`. Below that
  prefix, ref components use only `A-Z`, `a-z`, `0-9`, `-`, `_` and
  `.`, don't start with `.` or end with `.` or `.lock`, and hold no
  `..`. A component is at most 255 bytes and a ref at most 1024. Object
  IDs must have the length of the landing repository's hash. `shallow`
  lines are refused. The first command must ask for `side-band-64k`,
  so git sends its messages and those of `index-pack` and the
  pre-receive check to the guest inside the protocol, not on its own
  standard error. The filter reads no byte past the flush that ends
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
  use 2 GiB of memory (X26-pre-receive-check). Git's own limit and a
  scanner in `wb-hostd` bound it.

  Git's own limit, `GIT_ALLOC_LIMIT` (settings above), bounds each
  allocation: a delta's result, its data and its base, a whole object,
  and the object table `index-pack` sizes from the header's count, which
  it refuses above 1,638,399 objects at 64 bytes per entry. Each such
  push failed in under 0.1 s with the git child under 16 MiB
  (X28-git-alloc-limit). It doesn't bound the work: deltas under the
  cap still cost git CPU time, 145 s for 640 of them from a 42 KiB pack,
  and a blob over `core.bigFileThreshold` streams through a fixed
  buffer without reaching the limit. Git's tests use the variable, and
  `git(1)` doesn't document it. So `wb-hostd` checks that its git
  honors it, before it confines itself, right after it resolves the git
  binary (S04-architecture). It runs that absolute path, the one
  `wb-git` later executes, as `git hash-object --stdin` with
  `GIT_ALLOC_LIMIT=1k`, `LC_ALL=C`, `GIT_CONFIG_NOSYSTEM=1` and
  `GIT_CONFIG_GLOBAL=/dev/null` in an otherwise scrubbed environment,
  as `wb-git` runs git, writes 2 KiB of zeros to its standard input and closes
  it, and waits at most 5 s. The check passes only when git exits with
  status 128 and its standard error holds `over limit`. Anything else
  (exit 0, another status or message, a timeout) makes `wb-hostd`
  refuse every push, and log and show why. Exit status alone isn't
  enough: an unparsable value also exits with 128
  (`failed to parse GIT_ALLOC_LIMIT`). The same check runs again,
  against the same path, wherever the minimum git version check runs
  ("Host git" below). Both host gits passed it, and with
  `GIT_ALLOC_LIMIT=0` both hashed the input, because git reads `0` as
  no limit (X28-git-alloc-limit, `results/x28-probe.txt`).

  A scanner in `wb-hostd` reads the pack after the ref filter and
  before git does, and bounds the work. It checks each entry's header
  before it forwards it, then forwards the rest of the entry as it
  reads it, so it doesn't hold an entry. It holds back the pack's
  checksum, as long as the landing repository's hash, until the whole
  pack has passed: `index-pack` resolves
  deltas, allocating their bases and results, only after the checksum
  arrives, and with the checksum left off it resolved none
  (X28-git-alloc-limit). On a refusal the scanner closes git's input
  without the checksum. The caps are fixed, not configurable, until a
  real repository needs more (B74-pre-receive-check). It refuses the
  push when one of these caps is exceeded:
  - wire bytes (1 GiB): the whole pack, counted while the scanner reads
    it. The refusal names the rule (NFR06-explained-refusals), and
    `receive.maxInputSize` at the same value is git's second layer. A
    pack that passes the per-push cap compresses to less than about
    1.15 GiB (zlib's bound for 1 GiB), so the wire cap refuses little
    that the per-push cap would pass. A session's pushes are much
    smaller, because `landing.git` borrows from `export.git`: one new
    commit was under 1 KiB (X07-git-round-trip). The largest push
    measured, the Go repository's whole history, is 439 MiB on the wire
    and passes this cap (X28-git-alloc-limit), but at about 9 GiB
    inflated (X26-pre-receive-check) the per-push cap refuses it. The
    scanner doesn't hold an entry. This cap bounds the quarantine's disk use and the scanner's time, not its
    memory. There is no per-entry wire bound;
  - per object (100 MiB): an object's inflated size and a delta's own
    inflated data length, from the entry header before it is forwarded,
    and a delta's result size and declared source size, from the start
    of its data as it is forwarded. Git's limit enforces the same cap,
    so this check is there to refuse with a named rule
    (NFR06-explained-refusals) rather than with git's `fatal:` line. A
    delta whose base is in `export.git` is held to the same cap through
    its declared source size. Git checks that size against the base it
    loads and fails the push when they differ. A residual stays: a
    delta can declare a small source size for a large object of the
    user's own repository, and git loads that object once, at most
    `GIT_ALLOC_LIMIT` bytes, before the push fails closed. That is
    accepted, because the content is the user's own;
  - per push (1 GiB): the sum of all object and delta result
    sizes, delta data lengths, and the declared source size of every
    REF_DELTA. It counts resolved sizes, not new bytes, so 40 edits of
    a 30 MiB file exceed it in a few KiB. The refusal names the cap and
    the total, says that the cap is fixed, and says to push fewer
    commits at a time. A REF_DELTA names its base by ID, and when the
    base isn't in the pack, `index-pack --fix-thin` reads it from
    `export.git`, hashes it and writes it into the quarantine, once for
    each distinct base. Counting every REF_DELTA's source size, without
    telling a base in the pack from one outside it, bounds that work by
    this cap. It counts a base shared by several deltas more than once,
    which is strict. Git's own client names a base in the same pack
    with an OFS_DELTA once `ofs-delta` is agreed, so in a normal push a
    REF_DELTA's base comes from `export.git`;
  - REF_DELTA entries (10,000): their number in one pack, which bounds
    how many bases `index-pack --fix-thin` reads from `export.git`
    however small each one is. Without it, each 34 bytes of a pack
    could name another base (security review of PR116), so a pack under
    the 1 GiB wire-byte cap could name as many as the object count cap
    allows, 1,000,000;
  - object count (1,000,000): the count in the pack header,
    checked before the scanner forwards the header. `index-pack`
    allocates its object table from that count, and the scanner keeps
    one size per entry in a slice of that length, 8 MB at the cap.

  An entry's size header is at most 9 bytes with a value under 2^60,
  as `unpack_object_header_buffer` reads it in git 2.56.0 on 64-bit. A
  delta's size fields are at most 10 bytes each, with values that fit in
  64 bits. A longer field, or one with bits past those limits, is
  malformed and refused. The scanner inflates each entry only to find
  the next one and to read a delta's sizes, discards what it inflates,
  and stops one byte past the declared size. Its memory is then its
  inflate state and buffers, which don't depend on the pack, plus the
  slice of entry sizes, which the object count cap bounds. After the
  pack's checksum it forwards nothing more and closes git's input. A push with an empty command list carries no pack, and
  the scanner doesn't run. The scanner is a parser of guest bytes, so
  it has a fuzz target (S11-verification-and-spikes). It logs each
  refusal with the rule, and each push it passes with its object count,
  wire bytes and total size. Git's limit is the second layer behind it:
  where the scanner and git read a pack differently, git still can't
  allocate more than the cap at once.
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
  rule name there. For the object caps it adds a third layer, after the
  scanner and git's limit, and for the ref checks it is the only one. At start,
  `wb-hostd` checks that the hooks directory holds exactly this one
  file, and refuses pushes when it doesn't.
- **Git's standard error.** With `side-band-64k`, `receive-pack` sends
  the messages of `index-pack` and the hook to the guest inside the
  protocol, and `wb-hostd` passes them on without reading them. What
  `receive-pack` still writes to its own standard error, such as a
  `fatal:` line, reaches `wb-hostd`. Its text depends on the pack,
  for example an fsck message that quotes a tag name or a
  `.gitmodules` value, so `wb-hostd` treats it as guest input. It
  keeps at most 4 KiB per push, replaces control characters, escape
  sequences and invalid UTF-8 with U+FFFD, and logs the result as one
  quoted field next to the rule that ended the push
  (SEC10-audit).
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
  item 6). The cap is 4 GiB, so that it holds at least one push at the
  1 GiB wire-byte cap with its quarantine, and the user can change it
  with `landing_size_cap` in the project's settings. When a push would pass the cap, `wb-hostd` runs the same
  repack and prune first. When `landing.git` is still over the cap, the
  push is refused with a message that names the cap and says to land or
  discard finished sessions. `wb land` retries a fetch that fails while
  a repack runs.
- **Bounds.** Next to the size limit, which counts compressed bytes
  only (SEC13-bounded-resources): a deadline and an idle timeout per
  connection, at most one `upload-pack` per session at a time, at most
  one `receive-pack` per project at a time (the landing lock above), and
  a wall-clock watchdog that stops the git child. For a push, the
  idle timeout is 60 s and the watchdog, which is also the
  connection's deadline, 5 minutes. Traffic in either direction counts
  as activity for the idle timeout. While `index-pack` resolves the
  pack, `receive-pack` sends a keepalive on the side band every
  `receive.keepAlive` seconds (git's default, 5 s), so resolution time
  doesn't trip it. The largest push measured, the Go
  repository's whole history with the size limit raised to 1 GiB,
  took 33 to 56 s in `receive-pack` (X28-git-alloc-limit), and a push
  under the scanner's caps resolves at most 1 GiB. The git gateway work
  sets the values for `upload-pack`. The quarantine of one push holds
  at most the pack (1 GiB on the wire) and the bases `--fix-thin`
  copies in (counted in the 1 GiB per-push total), about 2 GiB of disk,
  and with one `receive-pack` per project at a time that is per
  project. `GIT_ALLOC_LIMIT`
  bounds each allocation git makes while it unpacks, and the pack
  scanner's caps bound the total and the CPU time. With
  `receive.unpackLimit=1` and `pack.threads=1`, `index-pack` holds its
  object table, its delta base cache (`core.deltaBaseCacheLimit`), a
  base and a result at a time, and under `--strict` one fsck object
  record for each object and for each ID a tree or commit names. One
  tree or commit of up to 100 MiB can name millions of IDs,
  so that part can add a few hundred MiB (security review of PR116,
  not measured). `GIT_ALLOC_LIMIT` also bounds it: git keeps those
  records in one hash table of 8-byte slots that it doubles with
  `xcalloc` when it is half full. The largest table under the limit has
  8,388,608 slots (64 MiB), so a push that names more than about
  4.2 million distinct IDs fails with git's `fatal:` line (read from
  `grow_object_hash` in git 2.56.0, not measured). The peak measured under a 100 MiB cap
  was 207 MiB for large deltas (X26-pre-receive-check), and 146 MiB for
  a million objects of 3 bytes in 12 MiB (X28-git-alloc-limit). These are
  measurements, and the conformance suite checks them again
  (S11-verification-and-spikes).
- **Session end.** If the worktree has uncommitted changes, `wb-guestd`
  commits them to the session branch as a clearly marked WIP commit, then
  pushes. `wb` can also push mid-session.
- **Landing on the host.** `wb land <session>` runs the user's own
  `git fetch` from the landing repository into `wb/<session-id>` in the
  user's repository. It never checks out, merges, or runs anything. A
  fetch only writes objects and refs.
- **Repository lifecycle** (B41-git-data-scope, item 6). Git never
  collects garbage in `export.git` or `landing.git` by itself. Both have
  `gc.auto=0` and `maintenance.auto=false`, and `landing.git` has
  `receive.autogc=false`, so every repack and prune runs under the
  locks below.
  - `wb land` leaves the session's refs in `landing.git`, so `wb diff`
    and another `wb land --branch` still work. `wb discard <session>`
    deletes the session's refs under `refs/heads/wb/<session-id>/` from
    `landing.git`, and has `wb-guestd` delete the session's guest
    worktree, at the VM's next start when it isn't running. The audit
    log keeps the session's entries. After each `wb land` and
    `wb discard`, `wb-hostd` runs the landing cleanup above (repack and
    prune) under the landing lock.
  - Each project has an export lock in `wb-hostd`. The export update
    at session start and every repack or prune of `export.git` hold it
    alone. Each `upload-pack` from `export.git` holds it shared, so no
    object disappears under a running fetch.
  - `landing.git` borrows from `export.git`, and `receive-pack`
    advertises the tips of `export.git` to the pushing guest
    (`core.alternateRefsCommand` in `git-config(1)`). A push leaves out
    the objects those refs reach. A session branch can then depend on
    any object `export.git`'s refs reached at the time of the push,
    also one from a branch that is no longer selected. So `wb-hostd`
    doesn't remove objects from `export.git` while `landing.git` holds
    any session ref: it runs neither `git prune` nor `git repack -a -d`
    there. When `landing.git` holds none, the next export update first
    runs `git repack -a -d` and `git prune --expire=now` on
    `export.git`. It holds the export lock and the landing lock while
    it does, so that no push starts meanwhile. That removes the objects
    of branches that are no longer selected. In exchange, `export.git`
    keeps every branch exported since the project last had no sessions.
    `export.git` has no size cap, because what it holds comes from the
    user's own repository.

## Host git

Git on the host parses data the guest built: `receive-pack` and
`index-pack` parse each push into `landing.git`, and `wb land` runs the
user's own git on the same objects (SEC03-no-host-exec). Wraith Box
doesn't ship git, because git is GPL-2.0 and S10-tech-stack allows only
permissive licenses. It requires a minimum version instead
(B41-git-data-scope, item 4).

- **Minimum version.** Git 2.54.0, the oldest version the git spikes
  ran (Apple git 2.54.0 in X07-git-round-trip and X28-git-alloc-limit),
  as a constant in `wb` and `wb-hostd`. A git security release that
  fixes code the round trip runs (`upload-pack`, `fetch`,
  `receive-pack`, `index-pack`) raises it, by a change to this spec.
- **Where it is checked.** `wb-hostd` checks the git binary it
  resolved, before it confines itself (S04-architecture), and refuses
  every push and every session start when the check fails. `wb` checks
  the user's git, which it runs for the export update and `wb land`,
  and the binary `wb-hostd` resolved, in `wb setup`, at each session
  start and before each `wb land`. Each check runs the absolute path it
  checks as `git version` and reads `git version <major>.<minor>.<patch>`
  from the start of the output, so a suffix such as
  `(Apple Git-157)` is ignored. Output it can't read, a failed run, or a
  version below the minimum refuses, with a message that names the
  binary, its version and the minimum (NFR06-explained-refusals), and
  the decision is logged with the rule.
- **Kept on top.** The settings of `receive-pack` above (every object
  check an error, the size limit, `GIT_ALLOC_LIMIT`) and the pack
  scanner stay, whatever the version.
- **Later.** A receive side written in Go, in place of
  `git receive-pack`, is an option for a later version, not V1.

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

No secret scanner is chosen yet (S06-vm-lifecycle, "Sealing"). The
carry-in scan assumes it is precise enough on working trees that it
skips few files by mistake, and that is not measured. How `wb` hands the
export stream to `wb-hostd`, and which process writes `export.git`, is
I67's to write into this spec and S04-architecture.

**Status:** Draft
