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
    one byte, pinned by a unit test, and a check before `wb-hostd`
    confines itself that the resolved git honors it, which refuses
    pushes when it doesn't (also in S04-architecture);
  - the scanner forwards each entry as it reads it, after checking the
    entry's header, and holds back only the pack's checksum until the
    pack has passed. The per-entry wire-byte bound goes;
  - the scanner keeps its caps: whole-pack wire bytes, per object, per
    push and object count. Git's limit is a second layer for the
    per-object cap and the object table, and `receive.maxInputSize` for
    the wire bytes;
  - after review: every REF_DELTA's declared source size counts toward
    the per-push total, and a pack holds at most 10,000 REF_DELTA
    entries, which bounds the bases `index-pack --fix-thin` reads from
    `export.git`;
  - after review: the first push command must ask for `side-band-64k`,
    and what `receive-pack` writes to its own standard error is logged
    as guest input, cleaned and capped;
  - values for `receive.maxInputSize`, the idle timeout and the
    watchdog of a push.

  S11-verification-and-spikes gets conformance cases for each.
- **Controls touched:** SEC13-bounded-resources (memory, CPU, and disk
  are capped): strengthened. Git now bounds each allocation itself, so a
  scanner bug or a parser differential between the scanner and git no
  longer lets a pack ask for unbounded memory, and the work of
  `--fix-thin` is now capped. SEC10-audit (decisions are logged):
  strengthened, git's standard error is logged as a cleaned field.
  SEC03-no-host-exec (returned work runs nothing on the host):
  unchanged.
- **Assumed:**
  - that later gits keep honoring `GIT_ALLOC_LIMIT`. It isn't in
    `git(1)`, only in git's own tests (`t/t1050-large.sh`), so the
    start check and a conformance case watch for it;
  - that Git for Windows and Linux distributions' gits build the same
    `wrapper.c`. Only the two macOS gits ran here;
  - that `index-pack` keeps resolving deltas only after the checksum
    arrives. It did with both gits, and S11-verification-and-spikes
    checks it again;
  - the findings in "After review" below. They come from reading git,
    and no run here measured them.
- **Open decisions:**
  1. Keep the scanner's per-object comparisons although git now
     enforces the same cap. Recommended: keep them. They cost one
     comparison on a value the scanner reads anyway for the per-push
     total, and a refusal by the scanner names its rule in the log and
     to the guest (NFR06-explained-refusals). Git's refusal is a
     `fatal:` line with the byte count.
  2. Drop the per-entry wire-byte bound, because the scanner no longer
     holds an entry. Recommended: drop it, and keep the whole-pack
     wire-byte cap as a scanner rule.
  3. The whole-pack wire-byte cap and `receive.maxInputSize`: 1 GiB,
     the same as the per-push total. Recommended, because a pack that
     passes the per-push total compresses to less than about 1.15 GiB,
     and a session's pushes are far smaller (one new commit was under
     1 KiB in X07-git-round-trip). The Go whole-history push, 439 MiB on
     the wire, passes it, and the per-push total refuses it at about
     9 GiB inflated. A smaller value, such as X07's 64 MiB, would bound
     quarantine disk more tightly and could refuse a legitimate push
     that adds large files.
  4. The cap of 10,000 REF_DELTA entries per pack, a choice no measurement backs: a
     normal push has about one REF_DELTA for each changed file or directory
     whose old version is in `export.git`. Recommended: 10,000.
  5. The idle timeout (60 s) and the watchdog of a push (5 minutes).
     Chosen against the slowest push measured, not measured themselves: the Go
     repository's whole history took 33 to 56 s in `receive-pack`.
- **Brief:** B102-git-alloc-limit

## Conditions

1. **`wb-hostd` sets `GIT_ALLOC_LIMIT` on `receive-pack`, to the
   per-object cap plus one byte (104857601).** Git adds a byte for a
   terminating NUL to most buffers (`xmallocz`), so a limit equal to
   the cap refused an object of exactly 100 MiB, which the `pre-receive`
   check passes. At 104857601 that object passed and one byte more was
   refused, as the check does. The children of `receive-pack`
   (`index-pack`, the check's `git cat-file`) inherit it. Git reads `0`
   as no limit, so a unit test pins the value.
2. **`wb-hostd` checks that its git honors the limit, before it
   confines itself.** Right after it resolves the git binary
   (S04-architecture), it runs that absolute path as
   `git hash-object --stdin` with `GIT_ALLOC_LIMIT=1k` and `LC_ALL=C`,
   writes 2 KiB of zeros and closes the input, and waits at most 5 s.
   It passes only on exit status 128 with `over limit` on standard
   error, and anything else refuses every push. Both gits passed: exit
   128, `attempting to allocate 1025 over limit 1024`. Without the
   variable and with `GIT_ALLOC_LIMIT=0` both hashed the input. An
   unparsable value also exits with 128 (`failed to parse
   GIT_ALLOC_LIMIT`), so the exit status alone isn't enough
   (`probe-alloc-limit.sh`, `results/x28-probe.txt`; the security
   review of PR116 reproduced it). Git's tests use the variable and
   `git(1)` doesn't document it. A git that drops it must then fail
   closed.
3. **The scanner stays, for the per-push total and the object count.**
   Git's limit caps one allocation, not the work. 640 deltas of 96 MiB,
   each under the cap, took git 145 s of CPU, and 64 of them 18 s. At
   about 54 bytes each, a 16 MiB pack holds about 300,000 such deltas
   (inferred). A blob over `core.bigFileThreshold` (512 MiB) is
   streamed through a fixed buffer, so the limit doesn't stop it
   either: a 1 GiB blob of zeros cost 3.4 s of CPU and landed when the
   check was off. The scanner refuses the deltas on the per-push total,
   and the blob on the per-object cap from its entry header, before git
   resolves anything.
4. **The scanner forwards each entry as it reads it, after checking
   the entry's header, and holds back the pack's checksum.** Git
   allocates in two passes. While the pack streams in, `index-pack`
   allocates each whole object and each delta's data from the size in
   its entry header. Only after the checksum at the end does it resolve
   deltas, allocating their bases and results. With the checksum left
   off, git resolved no delta, even without the limit: every delta bomb
   failed with `early EOF` at 15 MiB or less and 0.05 s. Only the
   buffers sized from entry headers were allocated: a 400 MiB blob
   without the checksum still cost 407 MiB, and a delta with 128 MiB of
   its own data 143 MiB. So the scanner checks the entry header (type
   and size, and for a delta its data length) before it forwards it,
   and checks a delta's result and source sizes as they come out of its
   data. It forwards the checksum only after the whole pack has passed.
   The scanner then doesn't hold an entry, and its memory is its inflate state
   plus one size per entry, which the object count cap bounds. The
   per-entry wire-byte bound of X26-pre-receive-check, which bounded
   what the scanner held, is no longer needed. This needs
   `receive.unpackLimit=1`: `unpack-objects` resolves a delta as soon
   as its base has arrived.

## After review

Review of the first version of this result found gaps that the spike
didn't measure. S08-workspace-and-git closes each one, and
S11-verification-and-spikes adds a case for each.

- **Bases from `export.git`.** `index-pack --fix-thin` reads, hashes
  and writes into the quarantine one base for each distinct REF_DELTA
  base that isn't in the pack, before the pre-receive check runs. The
  security review counted about 490,000 such entries in 16 MiB. So the
  scanner counts the declared source size of every REF_DELTA toward
  the per-push total, and refuses a pack with more than 10,000
  REF_DELTA entries. Git checks a declared source size against the base
  it loads and fails the push on a mismatch, so a false size costs at
  most one base of at most `GIT_ALLOC_LIMIT` bytes.
- **Whole-pack wire bytes.** The first version dropped the scanner's
  wire-byte cap along with the per-entry one. The scanner keeps it, so
  the refusal names its rule, and `receive.maxInputSize` now has a
  value.
- **Git's standard error.** Without side-band, the messages of
  `index-pack` reach `wb-hostd` raw, and fsck messages quote names from
  the pack. The ref filter now requires `side-band-64k`, and
  `wb-hostd` cleans, caps and quotes what `receive-pack` still writes
  to its own standard error.
- **fsck records.** Under `--strict`, fsck keeps a record for each
  object and for each ID a tree or commit names. One tree or commit of
  100 MiB names millions of IDs, which can add a few hundred MiB to the
  207 MiB measured. S11-verification-and-spikes measures it when the
  conformance suite is built.
- **Hash length.** The checksum the scanner holds back is as long as
  the landing repository's hash, not a fixed 20 bytes.

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
The harness raised `receive.maxInputSize` to 1 GiB and the per-push
total to 1 TiB for these rows, as X26-pre-receive-check did.

## What changes in the specs

- S08-workspace-and-git: `GIT_ALLOC_LIMIT` in the `receive-pack`
  settings, the start check, the scanner's streaming with the checksum
  held back, the per-entry wire-byte bound removed and the whole-pack
  one kept, REF_DELTA source sizes in the per-push total and the
  REF_DELTA cap, `side-band-64k` and the handling of git's standard
  error, values for the size limit, idle timeout and watchdog, the
  paragraph that waited on I102 removed, and the bounds list.
- S04-architecture: the start check in the list of what `wb-hostd`
  does before it confines itself.
- S11-verification-and-spikes: conformance cases for the limit with the
  scanner off, the start check with stand-in gits, a pack without its
  checksum, REF_DELTA sources and count, the wire-byte cap, a large
  tree and commit, side-band and standard error, and a unit test that
  pins the limit.

## Code

Throwaway code on branch `spike/x28-git-alloc-limit`, commit
[`63e150c`](https://github.com/wraithbox/wraithbox/tree/63e150cb38e9d3893606721e17bdbdf4149a8059/spikes/x28-git-alloc-limit):
the X26 harness with two new phases (`cmd/x26/alloc.go`), the start
check (`probe-alloc-limit.sh`), and the output of each run under
`results/x28-*.txt`.

**Status:** Answered 2026-10-06: yes, with conditions
