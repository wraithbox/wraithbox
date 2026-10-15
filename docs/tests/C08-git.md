# C08 - Git

**Purpose:** The test cases for the git round trip: the transport, the
pack scanner and the pre-receive check, git's own limits behind them,
and the data scope of what a session gets from the host repository.

**Verifies:** S08-workspace-and-git ("Bounds", and the data scope of
B41-git-data-scope),
X07-git-round-trip, X26-pre-receive-check, X28-git-alloc-limit

## Unit tests

- The environment builder of `wb-hostd` sets
  `GIT_ALLOC_LIMIT=104857601` for `receive-pack`.

## Fuzzing

- The git transport framing: the request header and the push command
  list (X07-git-round-trip).
- The input of the pre-receive check (X26-pre-receive-check).
- The pack scanner in front of `receive-pack`. Its target checks that:
  - what it forwards equals its input when a pack passes, and is a
    prefix of it when one is refused;
  - a refused prefix never holds the pack's checksum (as long as the
    landing repository's hash);
  - the scanner's heap stays under a fixed bound plus its slice of
    entry sizes, which the object count cap bounds
    (X28-git-alloc-limit).

  Its seeds include REF_DELTA entries, delta chains, empty zlib stored
  blocks, and packs written by zlib-ng at level 1 and with
  `core.compression=0`.
- A differential test passes the scanner's corpus and the generated
  packs to `git index-pack --stdin --strict`. The scanner and git must agree on
  entry boundaries and size fields, and every pack the scanner refuses
  as malformed git refuses too.

## Conformance

### The transport

- Write into the host repository through the git transport.
- Fetch an object outside the session's branch by its ID.
- Push outside `refs/heads/wb/<session-id>/`, or a tree with a `.git`
  entry.
- Push with a first command that doesn't ask for `side-band-64k`, and
  see the ref filter refuse it.
- With a pack that makes `receive-pack` write escape sequences and more
  than 4 KiB to its standard error, see the log hold one quoted field
  of at most 4 KiB with none of them.
- Check that the landing repository holds none of a refused push's
  objects (X26-pre-receive-check): right after the push for each
  refusal of the pre-receive check (a ref too long for the file system,
  a stale old object ID, directory/file and case clashes, an object
  over the cap), and after the cleanup for a push that failed later.
- Kill `receive-pack` in the middle of a push, and see the cleanup
  leave no `objects/tmp_objdir-*` and no `*.lock` under
  `refs/heads/wb/`.

### The pack scanner

Push each of these, and see the scanner refuse it with its rule before
the pack's checksum reaches git, and so before git resolves any delta,
with the memory of the git child and of `wb-hostd` measured
(X26-pre-receive-check, X28-git-alloc-limit):

- a delta whose result, source size or own data length is over the
  per-object cap;
- a blob whose inflated size is over it;
- objects whose sizes add up to more than the per-push cap;
- REF_DELTA entries whose declared source sizes alone add up to more
  than the per-push cap;
- 10,001 REF_DELTA entries against distinct small bases in
  `export.git`, refused before git reads a base;
- a pack header that declares more objects than the count cap, with no
  entries after it;
- an entry that declares a size of 1 byte, followed by empty zlib
  stored blocks until the pack is one byte over the 1 GiB wire-byte
  cap, which the scanner refuses on that cap while its memory doesn't
  grow.

Under the caps, the git child's peak memory stays near what
X26-pre-receive-check and X28-git-alloc-limit measured, 207 MiB at a
100 MiB per-object cap.

- Push one tree of 100 MiB, and one commit with 100 MiB of `parent`
  lines, each under the caps, and record the git child's peak memory
  when the conformance suite is built. These cases have no measured
  value yet (S08-workspace-and-git, "Bounds").

### Git's own limits, with the scanner off

With the scanner off and `receive.unpackLimit=1`, as
S08-workspace-and-git sets it:

- push a pack one byte over 1 GiB, and see git refuse it on
  `receive.maxInputSize`;
- push the scanner's packs, and see git refuse each one that asks for
  more than the per-object cap in one allocation, with the git child
  under 20 MiB, through `GIT_ALLOC_LIMIT` (X28-git-alloc-limit). An
  object of exactly the cap passes and one byte more is refused. A pack
  header that declares 1,638,400 objects or more is refused;
- push 64 deltas under the per-object cap without the pack's checksum,
  and see `index-pack` fail with no delta resolved, in under a second
  (X28-git-alloc-limit);
- push a pack that names more than 4.2 million distinct object IDs (for
  example trees that name many blobs), and see git fail when its object
  hash table would grow past `GIT_ALLOC_LIMIT` (S08-workspace-and-git,
  "Bounds").

### Time and the git binary

- Record the wall-clock time of the slowest pushes the caps allow,
  against the 5 minute watchdog: 1 GiB of empty zlib stored blocks
  behind one entry, 1 GiB of trees under `--strict`, and 1 GiB of bases
  that `--fix-thin` copies in from `export.git`, each just under its
  cap. Each one must finish well inside the watchdog.
- Start `wb-hostd` with a stand-in git that ignores `GIT_ALLOC_LIMIT`
  and exits with 0, one that exits with 128 and another message, and
  one that hangs past the 5 s timeout, and see every push refused with
  the reason.

### The git data scope of S08-workspace-and-git

Spec: B41-git-data-scope.

Each refusal with its rule in the audit log:

- Start a session with an untracked file that holds a token the scanner
  knows, and a tracked binary file changed to hold one. See the
  untracked file absent from the guest, the tracked file at its `HEAD`
  version, and both listed in the summary.
- Start a session with an untracked symbolic link to a host file, and
  see a link in the guest whose target is the string only, with none of
  the file's content.
- Give the host repository remotes with a token in the user info, an
  `ext::` URL, a `git+ssh://` URL, an IP-literal host and a URL that
  doesn't parse. See the guest get only cleaned `https://` URLs, and the
  rest listed as withheld.
- Start `wb` and `wb-hostd` with a stand-in git that reports a version
  below the minimum, and one that reports a release candidate of the
  minimum, and see both refused with the reason.
- Fill `landing.git` until its size plus the worst case of one push is
  over the cap, and see the next push refused before `receive-pack`
  starts.
- Remove a ref from the selection, start a session, and see a fetch of
  that ref's tip commit by ID refused, and the ref not advertised.

**Status:** Draft
