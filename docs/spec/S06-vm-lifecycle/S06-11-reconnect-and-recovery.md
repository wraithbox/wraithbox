# S06-11 - Reconnect, Loss and Recovery

**Purpose:** What happens when the host-guest link drops, when a
session is lost, and how a lost session's work is recovered or
discarded.

## Design

### Reconnect

When the host-guest link drops:

- `wb-guestd` stops the project processes (S13-guest-confinement,
  "Layer 1");
- `wb-hostd` connects to it again (S04-architecture).

`wb-hostd` waits 0.1 s before the first try, and doubles the wait
after each failed one, up to 5 s. It opens at most 20 connections to
one VM in a minute (SEC13-bounded-resources).

#### The list

After it connects, `wb-guestd` lists what `wb-hostd` hasn't
acknowledged:

- the sessions it holds;
- the reports of finished sessions (S06-09-session-lifecycle,
  "Ending");
- the cleanup records, each with its kill ID
  (S06-10-leftover-processes).

The list is guest input (SEC11-root-gains-nothing). `wb-hostd` refuses
the whole list when:

- it has more than 64 entries, held sessions and reports counted
  together;
- it has more cleanup records than project users in the VM;
- it names one ID twice, as a held session and as a report, or twice
  as either;
- it has two records with one kill ID.

It logs the refusal and closes the connection, which counts as a
failed try.

`wb-hostd` runs at most 64 sessions in one VM.

#### Answers to sessions and reports

For each entry, `wb-hostd` first checks the session ID format of
S05-cli ("Naming"). Then it looks for a session of this VM and this VM
generation only, and answers:

- "resume" for a running or paused session whose `wb` is still
  connected. The session goes back to running.
- "finish" for a running or paused session whose `wb` has gone, and
  for an ending session. The session goes to ending.
- "acknowledged" for a report of a session of this VM and this VM
  generation that `wb-hostd` has as running, paused, ending, or lost.
  The session ends with that report as at a normal end. The checks of
  S06-09-session-lifecycle ("Ending", "Returned work comes from the
  host") apply.
- "acknowledged" again for a report of a session that `wb-hostd`
  already has as ended. It logs that, and doesn't change the session.
- "end" for every other entry, a report included.

A lost session of this VM gets "finish" from recovery ("Recovery"
below).

On "finish", `wb-guestd`:

1. hangs up as in S06-09-session-lifecycle, "Closing the terminal ends
   the session", if `claude` still runs;
2. kills every process left in the session, a stopped WIP commit or
   push included;
3. removes the stale `index.lock` that a killed commit left
   (S06-09-session-lifecycle, "As the project user");
4. commits and pushes WIP again.

"end" goes to these entries:

- a starting session, whose start has failed;
- a session of another VM;
- a running, paused or ending session of another VM generation;
- an ended session;
- a malformed ID;
- an ID that `wb-hostd` has no record of.

On "end", `wb-guestd` kills every process left in that session, a
stopped WIP commit or push included. It doesn't commit or push. It
deletes the entry's report and recorded POSIX session ID. For a
session whose start failed, or that was discarded, it also removes
the worktree and its history link. `wb-hostd` logs each such entry
with the rule.

#### Answers to cleanup records

A cleanup record gets "acknowledged" when it passes the checks of
S06-10-leftover-processes, or was already acknowledged. Otherwise it
gets "end", which deletes it.

Every entry gets one of these answers, so every report and record is
cleared.

#### Sessions the list doesn't hold

`wb-guestd` resumes a session only on "resume".

A session of this VM that `wb-hostd` has as running, paused or ending
is lost when the list doesn't hold it, as a held session or as a
report. A starting session isn't in that rule, because a link drop
already failed its start.

A false list can only stop, keep, or end sessions in that guest.

`wb` doesn't print anything while a session is paused.

### Pause limit

A session that is paused for 30 seconds of host time awake is lost.
Time the host spends asleep doesn't count.

A restore reconnects in about 0.2 s (X18-vsock-handoff). A
`wb-guestd` restart loses the session anyway ("Lost" below). So the
limit only has to outlast a slow reconnect.

### Lost

A session is lost:

- when `wb-vmd` reports its VM stopped;
- when its `wb-guestd` exits. The reconnected `wb-guestd` doesn't list
  it, and a new `wb-guestd` process never takes over an earlier one's
  sessions;
- when `wb-hostd` exits, which stops every VM;
- at the pause limit.

A `wb-guestd` exit closes the PTYs it holds. So `claude` gets a hangup
from the guest kernel.

`wb` prints a `wraith box:` line that names the session, and says its
WIP returns at the next connection to the VM. Then it exits with 255
(S05-cli).

When `wb-hostd` starts, it marks as lost every session it has as
starting, running, paused or ending.

#### What a lost session counts as

A lost session counts as ended for these:

- the effective policy of S07-egress-gateway. The project's grants
  leave the VM when no other session of the project is starting,
  running, paused or ending there;
- `wb vm stop` and `wb vm suspend`.

A lost session still counts as a session for the lock and the kill of
a project's last session (S13-guest-confinement, "Layer 1"). So the
project user is locked only after its lost sessions have ended. They
end by recovery, `wb discard`, `wb project rm` or the deadline.

### Recovery

A lost session's worktree stays on the data disk.

At the next connection to a `wb-guestd` in that VM, `wb-hostd` sends
"finish" for each lost session of the VM that it has a record of,
whatever else the list holds. That connection is after a restart, or
at the next VM start. Recovery skips a lost session whose report the
list holds. "acknowledged" ends that one instead ("Reconnect").

On "finish", `wb-guestd` kills what is left of the session's
processes. Then it commits and pushes WIP, as at a normal end.

During recovery, `wb-hostd`:

- accepts a push for the lost session only to its own branch
  (S08-03-pushes, "Ref restriction");
- doesn't give network grants on account of the lost session.

The session is then ended, with "lost" as its exit status in `wb
sessions`. When this was the project's last session in the VM, the
project user is then locked and its processes killed
(S13-guest-confinement, "Layer 1").

A lost session stays live for `export.git` until it ends
(S08-02-export-repository). So its WIP push still has its base.

### Discarding a lost session

`wb discard` on a lost session ends it with no recovery push. So does
`wb project rm`, for each lost session of the project. `wb-hostd`:

1. takes the project's landing lock (S08-05-push-limits, "Landing
   lock");
2. records the session as ended and discarded;
3. only then deletes its refs.

A recovery push that already holds the lock finishes first. Its refs
are deleted after it. A later one is refused, because the session is
no longer live.

At the next connection to the VM, `wb-guestd` gets "end" for the
session. It removes the worktree and its history link.

## Decisions

### A `wb-hostd` exit stops every VM

- **Decided:** I48.

**Status:** Draft
