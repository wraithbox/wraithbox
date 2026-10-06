# X28 - git's allocation cap against delta bombs

Brief: B102-git-alloc-limit

**Question:** Does `GIT_ALLOC_LIMIT` in `receive-pack`'s environment
make both host gits refuse each delta bomb of X26-pre-receive-check
before allocating its result? If so, git bounds per-object memory and
the Go pack scanner of S08-workspace-and-git shrinks to the per-push
caps (SEC13-bounded-resources).

**Answer:** Yes, with conditions. With `GIT_ALLOC_LIMIT=100m`, Homebrew
git 2.56.0 and Apple git 2.54.0 refused every pack of X26 that asks for
more than the limit in one allocation before making it: a delta's result
(256 MiB to 2 GiB), a delta's own data, a delta's base from the
borrowed repository, a whole object, and the object table that
`index-pack` sizes from the count in the pack header. With the S08
settings each of these pushes failed in 0.02 to 0.06 s with the git
child at 15 MiB or less, and left nothing
in `landing.git`. The limit refused no real push: the Go repository's
whole history and a one-commit push passed as before. It doesn't bound
CPU time. 640 deltas of 96 MiB, each under the limit, cost git 145 s of
CPU from a 42 KiB pack before the `pre-receive` check refused them on
the per-push total. So the scanner stays, for the object count and the
per-push total, but it no longer has to hold an entry back to protect
git's memory.

## For review

- **Decides:** what bounds the memory git uses while it unpacks a
  push, and what is left of the pack scanner.
- **You are approving:** the S08-workspace-and-git changes in this pull
  request:
  - `GIT_ALLOC_LIMIT` set on `receive-pack` to the per-object cap plus
    one byte, and a check at start that the resolved git honors it,
    which refuses pushes when it doesn't;
  - the scanner forwards each entry as it reads it, after checking the
    entry's header, and holds back only the pack's 20-byte checksum
    until the pack has passed. The per-entry wire-byte bound goes;
  - the scanner keeps its caps: object count, per object, and per
    push. Git's limit is a second layer for the per-object cap and the
    object table.

  S11-verification-and-spikes gets conformance cases for the limit and
  for the start check.
- **Controls touched:** SEC13-bounded-resources (memory, CPU, and disk
  are capped): strengthened. Git now bounds each allocation itself, so a
  scanner bug or a parser differential between the scanner and git no
  longer lets a pack ask for unbounded memory. SEC03-no-host-exec
  (returned work runs nothing on the host): unchanged.
- **Assumed:**
  - that later gits keep honoring `GIT_ALLOC_LIMIT`. It isn't in
    `git(1)`, only in git's own tests (`t/t1050-large.sh`), so the
    start check and a conformance case watch for it;
  - that Git for Windows and Linux distributions' gits build the same
    `wrapper.c`. Only the two macOS gits ran here;
  - that `index-pack` keeps resolving deltas only after the checksum
    arrives. It did with both gits, and S11-verification-and-spikes
    checks it again.
- **Open decisions:**
  1. Keep the scanner's per-object comparisons although git now
     enforces the same cap. Recommended: keep them. They cost one
     comparison on a value the scanner reads anyway for the per-push
     total, and a refusal by the scanner names its rule in the log and
     to the guest (NFR06-explained-refusals). Git's refusal is a
     `fatal:` line with the byte count.
  2. Drop the per-entry wire-byte bound, because the scanner no longer
     holds an entry. Recommended: drop it. Git's `receive.maxInputSize`
     still bounds the whole pack.
- **Brief:** B102-git-alloc-limit

## Conditions

1. **`wb-hostd` sets `GIT_ALLOC_LIMIT` on `receive-pack`, to the
   per-object cap plus one byte (104857601).** Git adds a byte for a
   terminating NUL to most buffers (`xmallocz`), so a limit equal to
   the cap refused an object of exactly 100 MiB, which the `pre-receive`
   check passes. At 104857601 that object passed and one byte more was
   refused, as the check does. The children of `receive-pack`
   (`index-pack`, the check's `git cat-file`) inherit it.
2. **`wb-hostd` checks at start that its git honors the limit.** After
   it resolves the git binary (S04-architecture), it runs
   `git hash-object --stdin` with `GIT_ALLOC_LIMIT=1k` on 2 KiB of
   input and expects git to fail with `over limit`. Both gits did
   (exit 128, `attempting to allocate 1025 over limit 1024`), and
   hashed the same input without the variable. If git succeeds,
   `wb-hostd` refuses pushes and says why. Git's tests use the
   variable and `git(1)` doesn't document it. A git that drops it must
   then fail closed.
3. **The scanner stays, for the per-push total and the object count.**
   Git's limit caps one allocation, not the work. 640 deltas of 96 MiB,
   each under the cap, took git 145 s of CPU, and 64 of them 18 s. At
   about 54 bytes each, a 16 MiB pack holds about 300,000 such deltas
   (inferred). A blob over
   `core.bigFileThreshold` (512 MiB) is streamed through a fixed buffer,
   so the limit doesn't stop it either: a 1 GiB blob of zeros cost 3.4 s
   of CPU and landed when the check was off. The scanner refuses both
   on the per-push total before git resolves anything.
4. **The scanner forwards each entry as it reads it, after checking
   the entry's header, and holds back the pack's checksum.** Git
   allocates in two passes. While the pack streams in, `index-pack`
   allocates each whole object and each delta's data from the size in
   its entry header. Only after the 20-byte checksum at the end does it
   resolve deltas, allocating their bases and results. With the
   checksum left off, git resolved no delta, even without the limit:
   every delta bomb failed with `early EOF` at 15 MiB or less and
   0.05 s. Only the buffers sized from entry headers were allocated: a
   400 MiB blob without the checksum still cost 407 MiB, and a delta
   with 128 MiB of its own data 143 MiB. So the scanner checks the entry header (type and size, and
   for a delta its data length) before it forwards it, and checks a
   delta's result and source sizes as they come out of its data. It
   forwards the checksum only after the whole pack has passed. The
   scanner then doesn't hold an entry, and its memory doesn't depend on the
   pack. The per-entry wire-byte bound of X26-pre-receive-check, which
   bounded what the scanner held, is no longer needed.

## Memory and time

`ru_maxrss` and user plus system time of `receive-pack` with its
children. "S08" is `receive.unpackLimit=1`, `pack.threads=1` and the
`pre-receive` check, as S08-workspace-and-git sets them. The numbers
are Homebrew git's. Apple git gave the same result in every row, with
peak memory within 4 MiB. Every refused push left nothing in
`landing.git` and no quarantine directory.

| Pack | Wire size | S08, no limit | S08 with `GIT_ALLOC_LIMIT=100m` |
|---|---|---|---|
| One delta, result 256 MiB | 8 KiB | 271 MiB, 0.9 s, check refused | refused by git, 15 MiB, 0.05 s |
| One delta, result 2 GiB | 8 KiB | 2,063 MiB, 6.3 s, check refused | refused by git, 15 MiB, 0.05 s |
| 8 deltas of 256 MiB | 9 KiB | 527 MiB, 6.3 s, check refused | refused by git, 15 MiB, 0.05 s |
| One delta, result 100 MiB | 8 KiB | 115 MiB, accepted | refused by git (needs 100 MiB + 1 byte) |
| Same, limit 104857601 | 8 KiB | | 115 MiB, accepted |
| One delta, result 100 MiB + 1 byte, limit 104857601 | 8 KiB | 115 MiB, check refused | refused by git, 15 MiB |
| One delta with 128 MiB of its own data (result 64 MiB) | 135 KiB | 207 MiB, accepted | refused by git, 15 MiB, 0.05 s |
| REF_DELTA against a 200 MiB blob in the borrowed repository | 230 B | 208 MiB, 1.0 s, check refused | refused by git, 7 MiB, 0.03 s |
| Blob of 400 MiB zeros | 397 KiB | 407 MiB, 1.4 s, check refused | refused by git, 7 MiB, 0.02 s |
| Blob of 1 GiB zeros | 1 MiB | 7 MiB, 3.4 s, check refused | same: streamed, not refused by git |
| 8 deltas of 96 MiB | 9 KiB | 207 MiB, 2.3 s, accepted | same |
| Chain of 8 deltas of 96 MiB | 9 KiB | 207 MiB, accepted | same |
| 64 deltas of 96 MiB (6 GiB together) | 12 KiB | 206 MiB, 17 s, check refused on the total | same |
| 640 deltas of 96 MiB (60 GiB together) | 42 KiB | not run | 207 MiB, 145 s, check refused on the total |
| 1,000,000 blobs of 3 bytes | 12 MiB | 146 MiB, 2.3 s, accepted | same |

With git's defaults and the limit, `unpack-objects` (pushes under 100
objects) refused the same packs at the same sizes. The CPU times of the
deltas under the cap ran with other jobs on the machine. The 64-delta
row took 14 s in X26-pre-receive-check.

**The object table.** `index-pack` allocates `(count + 1) × 64` bytes
from the pack header's count before it reads an entry. Under
104857600 that admits counts up to 1,638,399:

| Count in the header (pack holds 3 objects) | No limit | `GIT_ALLOC_LIMIT=100m` |
|---|---|---|
| 1,000,000 | `early EOF`, 7 MiB | same |
| 1,638,399 | `early EOF`, 7 MiB | same |
| 1,638,400 | `early EOF`, 7 MiB | refused: asks for 104857664 bytes |
| 10,000,000 and 100,000,000 | `early EOF`, 7 MiB | refused |
| 4,294,967,295 | refused: `size_t overflow` | same |

Without the limit, a count of 100 million cost 7 MiB, not the 6 GiB
the table asks for. The likely reason (inferred, not traced) is that
the system backs zeroed pages only when they are written, and git
writes only the rows of entries it reads. Memory then grows
with the entries a pack holds, which `receive.maxInputSize` bounds: a
million blobs of 3 bytes in 12 MiB peaked at 146 MiB. So git bounds the table
by itself, and the scanner's count cap (1,000,000) stays as the cap
that gives an explained refusal.

## Cost

| Push | No limit | `GIT_ALLOC_LIMIT=100m` |
|---|---|---|
| Go repository, whole history (682,580 objects, 439 MiB) into an empty landing repository, Homebrew | accepted, 476 MiB, 44 s | accepted, 475 MiB, 37 s |
| Same, Apple | accepted, 472 MiB, 38 s | accepted, 472 MiB, 37 s |
| One new commit into a landing repository that borrows from the Go repository, Homebrew | 0.16 s | 0.16 s |
| Same, Apple | 0.22 s | 0.21 s |

Medians of three runs, Apple M2, 16 GB, macOS 27.0.1, the Go repository
at `1a1b710b4c`. The whole-history times spread from 33 to 56 s because
other jobs ran on the machine. They show that the limit refuses no real
push, not what the limit costs, which is one comparison per allocation.

## What changes in the specs

- S08-workspace-and-git: `GIT_ALLOC_LIMIT` in the `receive-pack`
  settings and the start check, the scanner's streaming with the
  checksum held back, the per-entry wire-byte bound removed, the
  paragraph that waited on I102 removed, and the bounds list.
- S11-verification-and-spikes: conformance cases for the limit with the
  scanner off, for the start check with a git that ignores the limit,
  and for a pack without its checksum. The wire-byte case changes to a
  check that the scanner's memory doesn't grow.

## Code

Throwaway code on branch `spike/x28-git-alloc-limit`, commit
[`f8527ce`](https://github.com/wraithbox/wraithbox/tree/f8527cebe94bea62fe4eec69747eda478cfd2d40/spikes/x28-git-alloc-limit):
the X26 harness with two new phases (`cmd/x26/alloc.go`), and the
output of each run under `results/x28-*.txt`.

**Status:** Answered 2026-10-06: yes, with conditions
