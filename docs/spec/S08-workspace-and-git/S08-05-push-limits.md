# S08-05 - Push Limits

**Purpose:** Cleanup after a push that failed, the size cap of `landing.git`, and
the time and memory bounds of the git gateway.

## Design

### Cleanup after a push that didn't end all `ok`

Some late failures
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

### Landing size cap

A successful push can also leave objects no ref
reaches: extra objects in its pack, and the bases that
`index-pack --fix-thin` copies in from `export.git`. A size cap per
project bounds `landing.git`, those included (B41-git-data-scope,
item 6). Before `receive-pack` starts, `wb-hostd` adds the size of
everything under `landing.git`, quarantine directories included, to
the worst case of one push. That worst case is a constant, the
wire-byte cap plus the per-push cap of the pack scanner above,
2 GiB with the current values: the pack itself, and the bases
`--fix-thin` copies in, which the per-push cap counts. When the sum is over the cap, it runs the same repack and
prune first. When the sum is still over the cap, the push is refused
with a message that names the cap and the sum and says to discard
finished sessions. A cap below the worst case of one push refuses
every push, with that reason. The cap is 4 GiB, twice the worst case,
which follows from the 1 GiB wire-byte and per-push caps that
X28-git-alloc-limit still has open. The user can change it with
`landing_size_cap` in the project's settings. `wb land` retries a
fetch that fails while a repack runs.

### Bounds

Next to the size limit, which counts compressed bytes
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
(C08-git).

**Status:** Draft
