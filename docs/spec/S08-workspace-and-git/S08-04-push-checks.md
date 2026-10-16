# S08-04 - Push Checks

**Purpose:** The checks that bound the memory and work of a push before
git unpacks it: git's allocation limit, the pack scanner, and the
pre-receive check.

## Design

### Pack scanner

Git sizes its buffers from numbers in the pack:

- the object count in the pack header;
- the header size of each object;
- the sizes at the start of each delta.

It allocates them while it unpacks, before any hook runs. An 8 KiB pack
made `receive-pack` use 2 GiB of memory (X26-pre-receive-check). These
layers bound a push:

| Layer | Bounds | Section |
|---|---|---|
| `GIT_ALLOC_LIMIT` | each allocation git makes | "Git's allocation limit" |
| The scanner in `wb-hostd` | the total size, and the work | "The scanner" |
| The pre-receive check | the objects in quarantine, and the refs | "Pre-receive check" |

### Git's allocation limit

`GIT_ALLOC_LIMIT` (S08-03-pushes, "Settings of `receive-pack`") bounds
each allocation git makes:

- a delta's result, its data and its base;
- a whole object;
- the object table that `index-pack` sizes from the header's count.
  Git refuses it above 1,638,399 objects, at 64 bytes per entry.

Each such push failed in under 0.1 s, with the git child under 16 MiB
(X28-git-alloc-limit).

The limit doesn't bound the work. Deltas under the cap still cost git
CPU time: 145 s for 640 of them from a 42 KiB pack. A blob over
`core.bigFileThreshold` streams through a fixed buffer without reaching
the limit.

### Checking that git honors the limit

Git's tests use `GIT_ALLOC_LIMIT`, but `git(1)` doesn't document it. So
`wb-hostd` checks that its git honors it. The check runs before
`wb-hostd` confines itself, right after it resolves the git binary
(S04-architecture). It runs again, against the same path, wherever the
minimum git version check runs (S08-08-host-git).

1. `wb-hostd` runs the absolute path that `wb-git` later executes, as
   `git hash-object --stdin`.
2. The environment holds `GIT_ALLOC_LIMIT=1k`, `LC_ALL=C`,
   `GIT_CONFIG_NOSYSTEM=1` and `GIT_CONFIG_GLOBAL=/dev/null`, and is
   otherwise scrubbed, as `wb-git` runs git.
3. `wb-hostd` writes 2 KiB of zeros to git's standard input, closes it,
   and waits at most 5 s.

The check passes only when git exits with status 128 and its standard
error holds `over limit`. Anything else makes `wb-hostd` refuse every
push, and log and show why: exit 0, another status or message, or a
timeout. Exit status alone isn't enough, because an unparsable value
also exits with 128 (`failed to parse GIT_ALLOC_LIMIT`).

Both host gits passed the check. With `GIT_ALLOC_LIMIT=0` both hashed
the input, because git reads `0` as no limit (X28-git-alloc-limit,
`results/x28-probe.txt`).

### The scanner

A scanner in `wb-hostd` reads the pack after the ref filter
(S08-03-pushes, "Ref restriction") and before git does. It bounds the
work.

- It checks each entry's header before it forwards it.
- It forwards the rest of the entry as it reads it, so it doesn't hold
  an entry.
- It holds back the pack's checksum until the whole pack has passed.
  The checksum is as long as the landing repository's hash.
- On a refusal it closes git's input without the checksum.

`index-pack` resolves deltas, allocating their bases and results, only
after the checksum arrives. With the checksum left off it resolved none
(X28-git-alloc-limit).

The scanner refuses the push when one of its caps is exceeded. The
caps are fixed, not configurable.

### The scanner's caps

| Cap | Value | Counts |
|---|---|---|
| Wire bytes | 256 MiB | the whole pack, while the scanner reads it |
| Per object | 100 MiB | an object's or a delta's sizes |
| Per push | 256 MiB | the sum of all resolved sizes |
| REF_DELTA entries | 10,000 | REF_DELTA entries in one pack |
| Object count | 1,000,000 | the count in the pack header |

#### Wire bytes

The wire-byte cap counts the whole pack while the scanner reads it. The
refusal names the rule (NFR06-explained-refusals).
`receive.maxInputSize` at the same value is git's second layer.

- The cap bounds the quarantine's disk use and the scanner's time, not
  its memory. The scanner doesn't hold an entry.
- There is no per-entry wire bound.
- A pack that passes the per-push cap compresses to less than about
  295 MiB, zlib's bound for 256 MiB. So the wire cap refuses little
  that the per-push cap would pass.

A session's pushes are much smaller, because `landing.git` borrows from
`export.git`. One new commit was under 1 KiB (X07-git-round-trip). The
largest push measured, the Go repository's whole history, is 439 MiB on
the wire (X28-git-alloc-limit), so the wire cap refuses it. A session
never pushes a whole history, because `landing.git` borrows it.

#### Per object

The per-object cap counts:

- an object's inflated size, and a delta's own inflated data length,
  from the entry header before it is forwarded;
- a delta's result size and declared source size, from the start of
  its data as it is forwarded.

Git's limit enforces the same cap. This check is there to refuse with a
named rule (NFR06-explained-refusals), rather than with git's `fatal:`
line.

A delta whose base is in `export.git` is held to the same cap through
its declared source size. Git checks that size against the base it
loads, and fails the push when they differ.

A residual stays. A delta can declare a small source size for a large
object of the user's own repository. Git then loads that object once,
at most `GIT_ALLOC_LIMIT` bytes, before the push fails closed. That is
accepted, because the content is the user's own.

#### Per push

The per-push cap counts the sum of:

- all object and delta result sizes;
- delta data lengths;
- the declared source size of every REF_DELTA.

It counts resolved sizes, not new bytes. So 40 edits of a 30 MiB file
exceed it in a few KiB. The refusal names the cap and the total, says
that the cap is fixed, and says to push fewer commits at a time.

A REF_DELTA names its base by ID. When the base isn't in the pack,
`index-pack --fix-thin` reads it from `export.git`, hashes it and
writes it into the quarantine, once for each distinct base. Counting
every REF_DELTA's source size bounds that work by this cap, without
telling a base in the pack from one outside it.

The count is strict: a base shared by more than one delta counts more than
once. Git's own client names a base in the same pack with an OFS_DELTA
once `ofs-delta` is agreed. So in a normal push a REF_DELTA's base
comes from `export.git`.

#### REF_DELTA entries

This cap bounds how many bases `index-pack --fix-thin` reads from
`export.git`, however small each one is. Without it, each 34 bytes of a
pack could name another base (security review of PR116). A pack under
the 256 MiB wire-byte cap could then name as many as the object count
cap allows, 1,000,000.

#### Object count

The count in the pack header is checked before the scanner forwards the
header. `index-pack` allocates its object table from that count. The
scanner keeps one size per entry in a slice of that length, 8 MB at the
cap.

### Entry headers

The scanner refuses a malformed header:

- An entry's size header is at most 9 bytes, with a value under 2^60.
  That is how `unpack_object_header_buffer` reads it in git 2.56.0 on
  64-bit.
- A delta's size fields are at most 10 bytes each, with values that fit
  in 64 bits.
- A longer field, or one with bits past those limits, is malformed.

The scanner inflates each entry only to find the next one and to read a
delta's sizes. It discards what it inflates, and stops one byte past
the declared size. Its memory is then its inflate state and buffers,
which don't depend on the pack, plus the slice of entry sizes, which
the object count cap bounds.

After the pack's checksum the scanner forwards nothing more, and closes
git's input. A push with an empty command list carries no pack, and the
scanner doesn't run.

The scanner is a parser of guest bytes, so it has a fuzz target
(C08-git). It logs each refusal with the rule. It logs each push it
passes with its object count, wire bytes, and total size.

Git's limit is the second layer behind the scanner. Where the scanner
and git read a pack differently, git still can't allocate more than the
cap at once.

### Pre-receive check

Git moves a pushed pack out of quarantine before some of its own ref
checks, which it makes only in `update()` (X07-git-round-trip). A small
`pre-receive` program makes those checks first, while the objects are
still in quarantine. It is separate from `wb-hostd`, and ships in the
hooks directory.

When it refuses, git deletes the quarantine and refuses every ref of
the push. Nothing of the push reaches `landing.git`
(X26-pre-receive-check). It refuses the push when, for any ref:

- the ref's lock file path, `<landing.git>/<ref>.lock`, is longer than
  the file system allows;
- a component plus `.lock` is longer than 255 bytes;
- the old object ID isn't the ref's current value. For a create, the
  ref must not exist;
- another ref is a directory prefix of it, or has it as one. That ref
  can exist, loose or packed, or be in the same push;
- another ref, existing or in the same push, has a path prefix equal
  to one of its own when case is folded, but not byte for byte. That
  would be the same file on APFS or NTFS;
- an object in the quarantine is over the per-object cap;
- all the objects together are over the per-push cap.

For the object caps it lists only the quarantine: `git cat-file
--batch-all-objects`, with `GIT_OBJECT_DIRECTORY` set to it and no
alternates. The bases that `index-pack --fix-thin` copies in from
`export.git` are in the quarantine, so they count.

How it runs:

- It runs under the landing lock (S08-05-push-limits, "Landing lock").
  The refs it reads are then the refs `update()` sees.
- Its limits come from the environment `wb-hostd` sets on
  `receive-pack`. A missing limit refuses everything.
- It writes each decision with its rule to a descriptor that `wb-hostd`
  opens and `receive-pack` passes on. It takes the descriptor's number
  from an environment variable `wb-hostd` sets. A missing variable or
  descriptor is a refusal.
- Its standard error goes to the guest, so it writes only the rule
  name there.
- At start, `wb-hostd` checks that the hooks directory holds exactly
  this one file. It refuses pushes when it doesn't.

For the object caps, the check is a third layer, after the scanner and
git's limit. For the ref checks it is the only one.

## Decisions

### Fixed caps

The scanner's caps are fixed, not configurable.

- **Why:** they stay fixed until a real repository needs more.
- **Decided:** B74-pre-receive-check.

### A scanner and git's limit, not one of them

- **Why:** git's limit bounds each allocation but not the work. The
  scanner bounds the work, and refuses with a named rule. Each covers
  where the other reads a pack differently (X26-pre-receive-check,
  X28-git-alloc-limit, B102-git-alloc-limit).

**Status:** Draft
