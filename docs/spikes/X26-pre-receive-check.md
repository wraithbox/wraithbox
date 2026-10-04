# X26 - Push checks before the quarantine lands

Brief: B74-pre-receive-check

**Question:** Can a `pre-receive` check that Wraith Box owns, never the
repository, refuse a push whose objects inflate past a cap, and repeat
the ref checks git only runs in `update()`? A refused push should then
leave nothing in `landing.git` (S08-workspace-and-git,
SEC13-bounded-resources). And what bounds the memory git uses while it
unpacks a push, which a hook runs too late to limit?

**Answer:** Yes, with conditions. A `pre-receive` check run from a
hooks directory that Wraith Box ships refused every case the question
lists, and `landing.git` kept none of a refused push's objects, with
Homebrew git 2.56.0 and Apple git 2.54.0. It cost 6 to 40 ms per
push. It does not bound memory: an 8 KiB pack drove `receive-pack` to
2 GiB of memory before any hook ran, and macOS refused `ulimit -v` and
`ulimit -d`. A Go pack scanner in front of `receive-pack` bounds it.
It reads the sizes git allocates from before git sees them, and refused
the same packs in 0.03 s. With it, `receive.unpackLimit=1` and
`pack.threads=1`, the largest peak measured in `index-pack` under a
100 MiB cap was 207 MiB. The cleanup after a failed push stays, as a
fallback: a leftover lock file still fails a push after the check
passed.

## For review

- **Decides:** how pushes into `landing.git` are bounded and checked
  before git moves their objects into it: a Go pack scanner between
  the ref filter and `receive-pack`, and a `pre-receive` check from a
  hooks directory in the installed bundle.
- **You are approving:** the S08-workspace-and-git changes in this pull
  request:
  - the pack scanner and its caps: wire bytes per pack and per entry,
    per object (inflated size, delta result, delta source and delta
    data length), per push, and the object count;
  - the `pre-receive` check, what it checks, and its decision log on a
    descriptor from `wb-hostd`;
  - `receive.unpackLimit=1` and `pack.threads=1` for `receive-pack`;
  - "repository hooks disabled" in place of "hooks disabled";
  - the cleanup after every push that didn't end all `ok`, now also
    removing leftover quarantines and lock files, and the landing size
    cap tied to it.

  S11-verification-and-spikes gets a fuzz target for the scanner and
  conformance cases.
- **Controls touched:** SEC13-bounded-resources (memory, CPU, and disk
  are capped): strengthened, the open point on inflated sizes is
  closed. SEC03-no-host-exec (returned work runs nothing on the host):
  kept. The hook is a program Wraith Box ships in its read-only bundle.
  The guest can't supply or change it, and repository hooks stay off.
- **Assumed:**
  - that the pack layouts measured (one large delta, many deltas of one
    base, a chain of deltas, large blobs) cover the ways git allocates
    while unpacking. The peak was measured, not derived from git's
    source;
  - that git deletes the quarantine whenever the scanner refuses in the
    middle of a stream. It did after every scanner refusal here, with
    git receiving a cut-off pack;
  - the review findings in "After review" below. They come from reading
    git and the spike's code, and no run here measured them;
  - that `receive-pack` passes an inherited descriptor on to its hook.
    The spike's hook logged to a file instead.
- **Open decisions:**
  1. The cap values: 100 MiB per object (GitHub refuses files over 100
     MiB), 1 GiB per push (configurable per project), and 1,000,000
     objects per pack. Recommended: these defaults.
  2. The scanner goes past I74, which asked about the check alone. It
     is a new parser of guest bytes in `wb-hostd`. Recommended: accept
     it, because the check alone leaves memory unbounded (B74-pre-receive-check,
     option B).
- **Brief:** B74-pre-receive-check

## Conditions

1. **A pack scanner in `wb-hostd` reads the pack before git does.**
   After the ref filter, it reads the pack header, every entry header
   and, for a delta, the source and result sizes at the start of the
   delta data. It holds each entry until the entry has passed, then
   forwards it to `receive-pack`. It refuses the push when one of these
   is over its cap: the object count in the header, an entry's wire
   bytes or the pack's, an object's size, a delta's source size, result
   size or own data length, or the running total. It inflates each
   entry only to find where the next one starts, stopping one byte past
   the declared size. A zlib bomb then costs at most the cap. It parses
   guest bytes, so it gets a fuzz target. The spike's scanner ran its
   fuzz target for a minute (2.4 million inputs) without a failure, but
   that scanner lacked the wire-byte and source-size limits, and its
   count cap had no default (below).
2. **`receive-pack` runs with `receive.unpackLimit=1` and
   `pack.threads=1`.** A chain of 8 deltas of 96 MiB each, all under
   the cap, peaked at 495 MiB in `index-pack` with git's default threads
   and 207 MiB with one. Pushes under 100 objects otherwise go to
   `unpack-objects`, which with `--strict` keeps a push's trees and
   commits in memory until it ends.
3. **A `pre-receive` check from a hooks directory in the installed
   bundle.** `core.hooksPath` is the absolute path of a read-only
   directory that holds only the check, a small program separate from
   `wb-hostd`, so no repository content can add a hook. It reads the
   commands on its stdin and refuses the whole push when:
   - the ref's lock file path (`<landing.git>/<ref>.lock`) is longer
     than the file system allows, or a component plus `.lock` is longer
     than 255 bytes;
   - the old object ID differs from the ref's value: a stale lease, a
     create of a ref that exists, an update of one that doesn't;
   - another ref, existing (loose or packed) or in the same push, is a
     directory prefix of this ref or has it as one;
   - another ref, existing or in the same push, has a path prefix that
     equals one of this ref's when case is folded, but not byte for
     byte;
   - an object in the quarantine is over the per-object cap, or all of
     the objects together over the total. It lists only the quarantine:
     `git cat-file --batch-all-objects --batch-check` with
     `GIT_OBJECT_DIRECTORY` set to the quarantine and
     `GIT_ALTERNATE_OBJECT_DIRECTORIES` empty.

   The check runs under the per-project landing lock. The refs it
   reads are then the refs `update()` sees. Its limits come from the
   environment `wb-hostd` sets on `receive-pack`, and a missing limit
   refuses everything. It writes each decision with its rule to a
   descriptor `wb-hostd` opened, and refuses when that descriptor is
   missing. The guest sees only the rule name, on standard error.
4. **The cleanup stays, as a fallback, and covers kills.** A leftover
   `feature.lock` from a crashed push passed the check and still failed
   in `update()`. The push's objects stayed behind. A killed
   `receive-pack` can leave a quarantine directory and lock files, so
   the cleanup removes those too, after every push that didn't end all
   `ok`. Successful pushes can also carry objects no ref reaches
   (below), which the landing size cap bounds.

## After review

Review of the first version of this result found gaps in the
scanner's design that the spike didn't run into. S08-workspace-and-git
closes each one. None was measured here, and S11-verification-and-spikes
adds a conformance case for each.

- **Wire bytes.** The scanner holds an entry until it passes, and zlib
  can consume input without producing output (empty stored blocks). A
  blob of size 1 followed by 512 MiB of empty stored blocks took the
  spike scanner's heap to 2,055 MiB (code review, at `077a8da`). The
  scanner now counts wire bytes itself, for the full pack and per entry
  (zlib's `deflateBound` for the declared size).
- **A delta's own data length.** `index-pack` allocates a delta's data
  from the entry header. The spike's scanner had the cap, and
  S08-workspace-and-git now names it and counts it in the per-push
  total.
- **A delta's source size.** A small `REF_DELTA` against a large blob
  in `export.git` makes git load that whole blob. The scanner refuses a
  source size over the per-object cap.
- **The object count.** `index-pack` allocates its object table from
  the count in the pack header before it reads an entry. Review
  estimated about 500 MiB for 10 million objects. The default cap of
  1,000,000 keeps that table, and the scanner's map of one size per
  entry, near 50 MiB each (inferred).
- **`unpack-objects --strict`.** It keeps every tree and commit of a
  push in memory, so its bound is the per-push total, not twice the
  per-object cap. `receive.unpackLimit=1` sends every push to
  `index-pack`.
- **Size fields.** The scanner reads each size field with git's own
  length limit, so it doesn't accept a field git refuses.

## Cases

Each case ran against a fresh landing repository holding one commit at
`refs/heads/wb/S1/main`, after the X07 ref filter had passed the command
list. Both gits gave the same result in every case. "Left" is what the
push added to `objects/` of `landing.git`.

| Case | No hook | With the check |
|---|---|---|
| Ref of 1,020 bytes (4 components of 250): passes the filter, lock file path too long | refused late, 3 objects left | refused, nothing left |
| One component of 253 bytes: passes the 255-byte rule, `.lock` doesn't fit | refused late, 3 left | refused, nothing left |
| Update with a stale old ID | refused late, 3 left | refused, nothing left |
| Create a ref that exists | refused late, 3 left | refused, nothing left |
| Update a ref that doesn't exist | refused late, 3 left | refused, nothing left |
| Push `main/x` while `main` exists | refused late, 3 left | refused, nothing left |
| Push `dir` while `dir/x` exists | refused late, 3 left | refused, nothing left |
| Push `p` and `p/q` together | both refused late, 3 left | refused, nothing left |
| Push `main/x` while `main` is a packed ref | **accepted**: both refs now exist | refused, nothing left |
| Push `MAIN` while `main` exists | refused late, 3 left | refused, nothing left |
| Push `DIR/y` while `dir/x` exists | **accepted**, stored as `dir/y` | refused, nothing left |
| Push `abc` and `ABC` together | `abc` accepted, `ABC` refused late | refused, nothing left |
| Push `MAIN` while `main` is a packed ref | **accepted**: both exist, a case-insensitive lookup of `main` finds `MAIN`'s file | refused, nothing left |
| A blob that inflates to 200 MiB (200 KiB on the wire) | accepted, blob landed | refused, nothing left |
| The pushed commit isn't in the pack | refused, nothing left | refused, nothing left |
| A leftover `feature.lock` blocks the ref | refused late, 3 left | **check passed**, refused late, 3 left |
| A valid push with one extra blob no ref reaches | accepted, extra blob landed | accepted, extra blob landed |

Git refused a push that names a missing object before it moved the
quarantine, so that case needs no check. In three cases git's files
backend accepted a push that left the refs inconsistent on a
case-insensitive file system. The check refuses those too.

**A reftable landing repository** (`--ref-format=reftable`, no
hook) accepted the long names and both case clashes, because its refs
aren't files. It refused the old-ID and directory/file cases late as
the files backend did, and applied `p/q` while refusing `p` in the
same push. It doesn't remove the need for the check. It also needs git
2.45 or later on the side that reads `landing.git`, which is the
user's own git in `wb land`.

**A check for objects no ref reaches** refused the extra blob, but it
also refused a normal push. Git's client sends a thin pack, whose
deltas use objects the receiver already has as their base. Once a push
holds 100 objects or more (`receive.unpackLimit`), `receive-pack`
keeps the pack, and `index-pack --fix-thin` copies those bases into
the quarantine. The spike forced that path with
`receive.unpackLimit=1`. On the
second push of a session nothing new reaches them, and the check
refused the push. On the first push into an empty landing repository
the same check walked the whole history, 3.8 s for the Go repository.
So stray objects stay a matter for the cleanup and the size cap of
`landing.git` (B41-git-data-scope, item 6).

## Memory while git unpacks

`receive-pack` hands the pack to `unpack-objects` (under 100 objects)
or `index-pack`. Both allocate a delta's result before any hook runs.
The table gives `ru_maxrss` of `receive-pack` with its children. Every
pack is far under the 16 MiB `receive.maxInputSize`. The numbers are
Homebrew git's, and Apple git was within 4 MiB in every row.

| Pack | Wire size | Without the scanner | With the scanner |
|---|---|---|---|
| One delta, result 256 MiB | 8 KiB | 271 MiB, landed or hook refused | refused in 0.03 s, 15 MiB |
| One delta, result 512 MiB | 8 KiB | 528 MiB | same |
| One delta, result 1 GiB | 8 KiB | 1,039 MiB | same |
| One delta, result 2 GiB | 8 KiB | 2,063 MiB | same |
| 8 deltas of 256 MiB | 9 KiB | 271 MiB (unpack-objects), 527 MiB (index-pack) | same |
| 64 deltas of 96 MiB (6 GiB together) | 12 KiB | 111 MiB, 33 s (unpack-objects), 207 MiB, 14 s (index-pack) | refused on the total in 0.03 s |
| Chain of 8 deltas of 96 MiB | 9 KiB | 208 MiB (unpack-objects), 495 MiB (index-pack), 207 MiB (index-pack, `pack.threads=1`) | under the cap: same as without |
| Blob of 400 MiB zeros | 397 KiB | 407 MiB | refused in 0.02 s, 7 MiB |
| Blob of 1 GiB zeros | 1 MiB | 8 MiB: git streams blobs over `core.bigFileThreshold` (512 MiB) | refused in 0.01 s |

Memory grew with the delta's declared result and nothing else, so a
pack under the input limit can ask for as much memory as the host has.
`ulimit -v` and `ulimit -d` both failed on macOS 27 with "cannot modify
limit: Invalid argument", so a resource limit on the git child is not
available there. Linux has `RLIMIT_AS` and cgroups, which this spike
didn't try.

The 15 MiB in the scanner rows is `receive-pack` alone. The scanner
runs in the harness process, as it would in `wb-hostd`, and its own
memory wasn't measured. It holds the entry it is checking, which the
spike's scanner didn't bound by wire bytes (see "After review"), and one
map entry per pack entry, which the object count cap bounds.

## Cost

Apple M2, 16 GB, macOS 27.0.1. Three runs each, median, Homebrew git
2.56.0 first and Apple git 2.54.0 second. The Go repository at
`6f5c275ebd`: 681,913 objects, 537,132 of them deltas, 438 MiB packed,
9,047 MiB inflated.

| Step | Homebrew | Apple |
|---|---|---|
| Scanner alone over the whole-history pack | 11.2 s | 11.2 s |
| Whole history into an empty landing repository, no check | 16.8 s | 18.5 s |
| Same, with the `pre-receive` check (the check alone) | 17.7 s (1.1 s) | 19.8 s (1.2 s) |
| Same, with the scanner in front as well | 26.6 s | 27.8 s |
| One new commit into a landing repository that borrows from the Go repository, no check | 0.14 s | 0.14 s |
| Same, with the check (the check alone) | 0.16 s (0.01 s) | 0.17 s (0.02 s) |
| Same, with the scanner in front as well | 0.16 s | 0.17 s |

The scanner runs on one core at about 0.8 GB of inflated data per
second, and caps the push it reads: under a 1 GiB total cap it stops
after at most about 1.3 s. The whole-history rows raise the input limit
to 1 GiB and the total cap to 1 TiB to time the full work. A session
never pushes that much, because the landing repository borrows from
`export.git` (X07-git-round-trip).

The check listed only the quarantine: 3 objects for a one-commit push
into a landing repository that borrows 680,000 objects.

## Interactions

- **I67 and the `wb-git` shim** (X23-sandboxed-daemons, condition 4).
  The check runs as a child of `receive-pack`, inside whatever confines
  git. The `wb-git-receive` profile must then let git execute the
  check's program from the installed bundle, and let the check run
  `git for-each-ref` and `git cat-file` on that one landing repository
  and its alternates. I67 is about reading the user's repository for
  `upload-pack`, and nothing here changes it.
- **I41, item 6.** Thin pushes copy borrowed bases into `landing.git`,
  and successful pushes can carry objects no ref reaches. The landing
  size cap and garbage collection bound both.

## What changes in the specs

- S08-workspace-and-git: the pack scanner and its caps, the
  `pre-receive` check and its hooks directory, `receive.unpackLimit=1`
  and `pack.threads=1`, "repository hooks disabled", object IDs that
  match the landing repository's hash, the cleanup after every push
  that didn't end all `ok`, the landing size cap, the bounds list, and
  the open point on inflated sizes removed.
- S11-verification-and-spikes: the scanner and the check's stdin join
  the fuzz list, with an oracle for the scanner, and conformance cases
  for the refusals here, the review findings and a killed push.

## Code

Throwaway code on branch `spike/x26-pre-receive-check`, commit
[`077a8da`](https://github.com/wraithbox/wraithbox/tree/077a8da13267b713f13b0b3bf3cd149df0830e85/spikes/x26-pre-receive-check):
the check, the scanner with its fuzz target, the pack builder, the
harness, and the output of each run under `results/`.

**Status:** Answered 2026-10-05: yes, with conditions
