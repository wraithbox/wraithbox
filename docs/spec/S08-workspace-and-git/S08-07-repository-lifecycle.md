# S08-07 - Repository Lifecycle

**Purpose:** When objects leave `export.git` and `landing.git`, and the
locks that order that work against fetches and pushes.

## Design

### No automatic garbage collection

Git never collects garbage in `export.git` or `landing.git` by itself:

- both have `gc.auto=0` and `maintenance.auto=false`;
- `landing.git` also has `receive.autogc=false`.

So every repack and prune runs under the locks below: the export lock
for `export.git`, and the landing lock for `landing.git`
(S08-05-push-limits, "Landing lock").

### Discard

`wb land` leaves the session's refs in `landing.git`, so `wb diff` and
another `wb land --branch` still work. No cleanup follows `wb land`.

`wb discard <session>`:

1. On a lost session, first ends the session with no recovery push,
   under the landing lock (S06-vm-lifecycle, "Discarding a lost
   session").
2. Deletes the session's refs under `refs/heads/wb/<session-id>/` from
   `landing.git`.
3. Has `wb-guestd` delete the session's guest worktree and its history
   link (S06-vm-lifecycle). When the VM isn't running, that happens at
   its next start.

After each `wb discard`, `wb-hostd` runs the landing cleanup, repack and
prune, under the landing lock (S08-05-push-limits, "Cleanup after a
failed push"). The audit log keeps the session's entries.

### Export lock

Each project has an export lock in `wb-hostd`.

- `wb` takes it alone, through `wb-hostd`'s local IPC. It takes it for
  the export update at session start, and for every repack or prune of
  `export.git`.
- Each `upload-pack` from `export.git` to the guest holds it shared. No
  object then disappears under a running fetch.
- The git gateway's connection deadline (S08-05-push-limits, "Bounds")
  ends such an `upload-pack`. A guest can't hold the lock past it.

The lock is one streaming call on `wb-hostd`'s local IPC endpoint,
named with the project ID.

- `wb-hostd` grants it once no `upload-pack` of the project holds it
  shared.
- From the moment `wb` asks, `wb-hostd` doesn't start another
  `upload-pack` of that project until `wb` lets go. The guest's fetches
  then can't keep `wb` waiting past one connection deadline.
- The grant lists the sessions of the project that are no longer live.
  The ref lists and the acknowledgment of a shrink ("Shrinking the
  selection") travel on the same call.
- `wb` holds the lock as long as the call is open. `wb-hostd` releases
  it when `wb` closes the call or its connection ends.
- The lock is only in `wb-hostd`'s memory, so a restart of `wb-hostd`
  doesn't leave a stale lock.

The call can end while `wb` holds the lock, because `wb-hostd` stopped
or `wb` lost the connection. `wb` then stops its git child, and refuses
the session start with that reason (NFR06-explained-refusals).

A fetch, delete or prune that stops part way leaves objects and refs in
`export.git`. The next update replaces, deletes, or prunes them. A
prune, finished or not, removes only objects that no ref of
`export.git` reaches.

The lock orders work and is not a control. A compromised `wb-hostd`
that grants it while an `upload-pack` runs only breaks that guest's
fetch. One that never grants it only stops session starts. What it
says can't make `wb` write anything but the selected refs, the deletes
of names it checked, and the prune.

### Borrowed objects

`landing.git` borrows from `export.git`. `receive-pack` advertises the
tips of `export.git` to the pushing guest (`core.alternateRefsCommand`
in `git-config(1)`). A push leaves out the objects those refs reach. A
session branch can then depend on any object that `export.git`'s refs
reached at the time of the push.

### Shrinking the selection

An export update deletes the refs of sessions that are no longer live,
and moves the plain names. It shrinks the selection when some object is
then no longer reachable from any ref of `export.git`.

`wb` finds a shrink with `git rev-list --objects` of the old tips, with
`--not` and the new ones. Any output is a shrink. The refs of every
live session stay (S08-02-export-repository, "Parallel sessions"). So
only objects that no live session selected can go.

Without a shrink, `wb` only deletes the refs. With one, `wb` holds the
export lock, and `wb-hostd` the landing lock, from the first of these
steps to the last. Neither a push nor a landing cleanup starts before
the prune ends.

1. `wb` sends `wb-hostd` the exact list of refs to delete. The fetch
   has already moved the plain names. The objects their old tips reach
   stay in `export.git` until the prune.
2. `wb-hostd` checks each name with `git check-ref-format`.
3. `wb-hostd` copies into `landing.git` the objects that its session
   refs reach and the remaining refs don't. It runs `git rev-list
   --objects` of the session refs, with `--not` and every ref of
   `export.git` not on the list, piped into `git pack-objects` without
   `--local`. `landing.git` then holds every object its refs need that
   the prune can remove.
4. `wb-hostd` acknowledges the list.
5. `wb` deletes exactly the refs on the list. Then it runs `git repack
   -a -d` and `git prune --expire=now` on `export.git`.

The objects of a ref that left the selection are then gone from
`export.git`. A later session can't fetch them by ID. A guest that
fetched them earlier still has them in the project user's clone,
because pruning only stops later fetches (T12-shared-export).

The copy adds to `landing.git`'s size only the objects of deselected
refs that a session branch still reaches. It counts toward the cap
(S08-05-push-limits, "Landing size cap"). `export.git` has no size cap,
because what it holds comes from the user's own repository.

### Cost of a shrink

The prune runs on the session start path, before the new session's
guest fetches. That costs NFR01-startup a full repack of `export.git`
whenever the selection shrinks. The project's pushes wait for the
landing lock meanwhile.

The cost isn't measured. As a rough guide, building `export.git` for
the Go repository took about 16 s (X07-git-round-trip).

When the user stays on one branch and only adds commits to it, the
base of an ended session is an ancestor of the new tip. Deleting its
refs then isn't a shrink, and the update doesn't prune.

## Decisions

### Repack and prune only under the locks

- **Why:** a prune that runs while a fetch or push is in flight can
  remove objects that the fetch or push needs.
- **Decided:** B41-git-data-scope, item 6.

**Status:** Draft
