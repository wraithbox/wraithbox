# S06-11 - Reconnect, Loss and Recovery

**Purpose:** What happens when the host-guest link drops, when a session is
lost, and how a lost session's work is recovered or discarded.

## Design

### Reconnect

When the host-guest link drops, `wb-guestd` stops the
project processes (S13-01-process-hardening), and `wb-hostd`
connects to it again (S04-architecture). It waits 0.1 s before the
first try, doubles the wait after each failed one up to 5 s, and
opens at most 20 connections to one VM in a minute
(SEC13-bounded-resources). After it connects, `wb-guestd` lists the
sessions it holds, the reports of finished sessions, and the cleanup
records, each with its kill ID, that `wb-hostd` hasn't acknowledged
(S06-09-session-lifecycle, "Ending", and S06-10-leftover-processes). The list is guest input
(SEC11-root-gains-nothing). `wb-hostd` refuses a list of more than
64 entries, held sessions and reports counted together, or with more
cleanup records than project users in the VM, whole. It
also refuses a list that names one id twice, as a held session and
as a report or twice as either, or that has two records with one
kill ID. It logs the refusal and closes the
connection, which counts as a failed try. It runs at most 64 sessions in one VM. For each
entry it checks the session ID format of S05-04-naming first,
then looks for a session of this VM and this VM generation only, and
answers:

- "resume" for a running or paused session whose `wb` is still
  connected. It goes back to running.
- "finish" for a running or paused session whose `wb` has gone, and
  for an ending session. It hangs up as in S06-09-session-lifecycle, "Closing the terminal ends the session", if `claude` still
  runs. Then it kills every process left in the session, a stopped
  WIP commit or push included, removes the stale `index.lock` a
  killed commit left (S06-09-session-lifecycle, "As the project user"), and commits and
  pushes WIP again. It goes to ending. A lost session of this VM
  gets "finish" from recovery (below).
- "acknowledged" for a report of a session of this VM and this VM
  generation that `wb-hostd` has as running, paused, ending or
  lost. The session ends with that report as at a normal end, under
  the checks of S06-09-session-lifecycle (S06-09-session-lifecycle, "Ending", "Returned work
  comes from the host").
- "acknowledged" again for a report of a session `wb-hostd` already
  has as ended, which it logs and doesn't change.
- "end" for every other entry, a report included: a starting session (the start has
  failed), a session of another VM, a running, paused or ending
  session of another VM generation, an ended session, a malformed
  id, and an id it has no record of. `wb-guestd` kills every
  process left in that session, a stopped WIP commit or push
  included, and doesn't commit or push. It deletes the entry's
  report and recorded POSIX session id. For
  a session whose start failed, or that was discarded, it also
  removes the worktree and its history link. `wb-hostd` logs each
  such entry with the rule.

#### Answers to cleanup records

A cleanup record gets "acknowledged" when it passes the checks of
S06-10-leftover-processes or was already acknowledged, and "end"
otherwise, which deletes it.

Every entry gets one of these answers, so every report and record is
cleared.

`wb-guestd` resumes a session only on "resume". A session of this VM
that `wb-hostd` has as running, paused or ending and the list
doesn't hold, as a held session or as a report, is lost. A starting
session isn't in that rule: a link drop already failed its start. A false list can only stop, keep or end
sessions in that guest. `wb` doesn't print anything while a session
is paused.

### Pause limit

A session that is paused for 30 seconds of host
time awake is lost. Time the host spends asleep doesn't count. A
restore reconnects in about 0.2 s (X18-vsock-handoff), and a
`wb-guestd` restart loses the session anyway ("Lost"), so the limit
only has to outlast a slow reconnect.

### Lost

A session is lost when `wb-vmd` reports its VM stopped,
when its `wb-guestd` exits (the reconnected `wb-guestd` doesn't list
it, and a new `wb-guestd` process never takes over an earlier one's
sessions), when `wb-hostd` exits, which stops every VM (decided on
I48), or at the pause limit. A `wb-guestd` exit closes the PTYs it
holds, so `claude` gets a hangup from the guest kernel. `wb` prints a
`wraith box:` line that names the session and says its WIP returns
at the next connection to the VM, and exits with 255 (S05-cli). When
`wb-hostd` starts, it marks every session it has as starting,
running, paused or ending as lost. For the effective policy of
S07-egress-gateway, and for `wb vm stop` and `wb vm suspend`, a lost
session counts as ended: its project's grants leave the VM when no
other session of the project is starting, running, paused or ending
there. For the lock and the kill of a project's last session
(S13-01-process-hardening), a lost session still counts as
a session, so the project user is locked only after its lost
sessions have ended, by recovery, `wb discard`, `wb project rm` or
the deadline.

### Recovery

A lost session's worktree stays on the data disk. At
the next connection to a `wb-guestd` in that VM, after a restart or
the next VM start, `wb-hostd` sends "finish" for each lost session
of the VM that it has a record of, whatever else the list holds.
Recovery skips a lost session whose report the list holds, which
"acknowledged" ends instead ("Reconnect"). On "finish",
`wb-guestd` kills what is left of the session's processes, then
commits and pushes WIP as at a normal end. During recovery
`wb-hostd` accepts a push for the lost session only to its own
branch (S08-03-pushes, "Ref restriction"), and gives no
network grants on account of the lost session. The session is then
ended, with "lost" as its exit status in `wb sessions`. When this
was the project's last session in the VM, the project user is then
locked and its processes killed (S13-01-process-hardening). A lost session stays live for
`export.git` until it ends (S08-02-export-repository), so its WIP push still has its base.

### Discarding a lost session

`wb discard` on a lost session, and
`wb project rm` for each lost session of the project, end it with no
recovery push. `wb-hostd` takes the project's landing lock
(S08-workspace-and-git), records the session as ended and discarded,
and only then deletes its refs. A recovery push that already holds
the lock finishes first, and its refs are deleted after it. A later
one is refused, because the session is no longer live. At the next
connection to the VM, `wb-guestd` gets "end" for it, and removes
the worktree and its history link.

**Status:** Draft
