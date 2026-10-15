# S08-04 - Push Checks

**Purpose:** The checks that bound the memory and work of a push before git
unpacks it: git's allocation limit, the pack scanner, and the
pre-receive check.

## Design

### Pack scanner

Git sizes its buffers from numbers in the pack: the
object count in the pack header, the header size of each object, and
the sizes at the start of each delta. It allocates them while it
unpacks, before any hook runs, so an 8 KiB pack made `receive-pack`
use 2 GiB of memory (X26-pre-receive-check). Git's own limit and a
scanner in `wb-hostd` bound it.

### Git's allocation limit

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
(S08-08-host-git). Both host gits passed it, and with
`GIT_ALLOC_LIMIT=0` both hashed the input, because git reads `0` as
no limit (X28-git-alloc-limit, `results/x28-probe.txt`).

### The scanner

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

### The scanner's caps

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

### Entry headers

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
it has a fuzz target (C08-git). It logs each
refusal with the rule, and each push it passes with its object count,
wire bytes and total size. Git's limit is the second layer behind it:
where the scanner and git read a pack differently, git still can't
allocate more than the cap at once.

### Pre-receive check

Git moves a pushed pack out of quarantine
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

**Status:** Draft
