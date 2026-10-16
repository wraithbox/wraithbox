# S08-05 - Push Limits

**Purpose:** Cleanup after a push that failed, the size cap of
`landing.git`, and the time and memory bounds of the git gateway.

## Design

### Cleanup after a failed push

Some late failures get past the pre-receive check (S08-04-push-checks).
A lock file left by a crashed push, or an I/O error, still fails a ref
in `update()` after git moved the pack (X26-pre-receive-check).

A `receive-pack` can also be killed: by the watchdog, a disconnect, or a
scanner refusal. It can then leave an `objects/tmp_objdir-*` quarantine
and `*.lock` files that repack and prune don't remove. `wb-hostd` stops
git with SIGTERM, which lets git remove its quarantine, and with
SIGKILL after a grace period.

So `wb-hostd` cleans `landing.git` after every push in which not every
ref reported `ok`. That includes kills, disconnects, and scanner
refusals. It cleans while no other push to the repository runs:

1. It removes `objects/tmp_objdir-*`.
2. It removes `*.lock` files under `refs/heads/wb/`, and
   `packed-refs.lock` at the repository's root.
3. It runs `git repack -a -d -l` and `git prune --expire=now`.

### Landing lock

A per-project lock in `wb-hostd` serializes the pushes to one landing
repository and its cleanup. Sessions share the repository
(FR05-parallel-sessions). Without the lock, a cleanup could prune a
concurrent push's objects in the moment between leaving quarantine and
its ref update.

### Landing size cap

A successful push can also leave objects that no ref reaches: extra
objects in its pack, and the bases that `index-pack --fix-thin` copies
in from `export.git`. A size cap per project bounds `landing.git`,
those objects included.

Before `receive-pack` starts, `wb-hostd` adds two sizes:

- everything under `landing.git`, quarantine directories included;
- the worst case of one push.

That worst case is a constant: the wire-byte cap plus the per-push cap
of the pack scanner (S08-04-push-checks), 2 GiB with the current values.
It covers the pack itself, and the bases `--fix-thin` copies in, which
the per-push cap counts.

1. When the sum is over the cap, `wb-hostd` runs the repack and prune
   of "Cleanup after a failed push" first.
2. When the sum is still over the cap, it refuses the push. The message
   names the cap and the sum, and says to discard finished sessions.

A cap below the worst case of one push refuses every push, with that
reason.

The cap is 4 GiB, twice the worst case. The user can change it with
`landing_size_cap` in the project's settings.

`wb land` retries a fetch that fails while a repack runs.

### Bounds

The size limit counts compressed bytes only (SEC13-bounded-resources).
Next to it, the git gateway has:

- a deadline and an idle timeout per connection;
- at most one `upload-pack` per session at a time;
- at most one `receive-pack` per project at a time, under the landing
  lock;
- a wall-clock watchdog that stops the git child.

#### Time

For a push, the idle timeout is 60 s. The watchdog, which is also the
connection's deadline, is 5 minutes. Traffic in either direction counts
as activity for the idle timeout.

While `index-pack` resolves the pack, `receive-pack` sends a keepalive
on the side band every `receive.keepAlive` seconds, git's default of
5 s. So resolution time doesn't trip the idle timeout.

The largest push measured took 33 to 56 s in `receive-pack`
(X28-git-alloc-limit). That was the Go repository's whole history, with
the size limit raised to 1 GiB. A push under the scanner's caps
resolves at most 1 GiB.

The git gateway work sets the values for `upload-pack`.

#### Disk

The quarantine of one push holds at most:

- the pack, 1 GiB on the wire;
- the bases `--fix-thin` copies in, counted in the 1 GiB per-push
  total.

That is about 2 GiB of disk. With one `receive-pack` per project at a
time, it is per project.

#### Memory

`GIT_ALLOC_LIMIT` bounds each allocation git makes while it unpacks.
The pack scanner's caps bound the total and the CPU time.

With `receive.unpackLimit=1` and `pack.threads=1`, `index-pack` holds:

- its object table;
- its delta base cache (`core.deltaBaseCacheLimit`);
- a base and a result at a time;
- under `--strict`, one fsck object record for each object, and for
  each ID a tree or commit names.

One tree or commit of up to 100 MiB can name millions of IDs. That part
can add a few hundred MiB (security review of PR116, not measured).

`GIT_ALLOC_LIMIT` also bounds those records. Git keeps them in one hash
table of 8-byte slots, which it doubles with `xcalloc` when it is half
full. The largest table under the limit has 8,388,608 slots (64 MiB).
So a push that names more than about 4.2 million distinct IDs fails
with git's `fatal:` line. This is read from `grow_object_hash` in git
2.56.0, and not measured.

The peaks measured under a 100 MiB cap:

- 207 MiB for large deltas (X26-pre-receive-check);
- 146 MiB for a million objects of 3 bytes in 12 MiB
  (X28-git-alloc-limit).

The conformance suite checks these measurements again (C08-git).

## Decisions

### A size cap on `landing.git`

- **Why:** a successful push can leave objects that no ref reaches. Without a
  cap the repository grows.
- **Decided:** B41-git-data-scope, item 6.

### 4 GiB

The cap is twice the worst case of one push.

- **Why:** the worst case follows from the 1 GiB wire-byte and per-push
  caps, which X28-git-alloc-limit still has open.

**Status:** Draft
