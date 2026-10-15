# S06-09 - Session Lifecycle

**Purpose:** A session's states from `wb claude` to its summary, how it starts
and ends, its deadlines, and when its VM may be saved.

## Design

A session runs from `wb claude` to its summary. `wb-hostd` keeps each
session's record and state in `state.db` (S04-architecture, "Host state"), with the VM and the VM generation it runs in. `wb-guestd`
holds the session's processes and, in interactive use, its terminal
device (PTY). The maintainer decided on I46 (B46-session-lifecycle)
that closing the terminal ends the session. V1 has no detach and no
`wb attach`, and a session has one client, the `wb` process that
started it.

![Session states. A session starts, runs, and ends with a WIP commit and push. A start that fails or times out ends at once. A dropped host-guest link pauses a running session, which resumes or finishes when the link returns. A stopped VM or daemon, or a pause past 30 seconds, loses a session, and a lost session ends by recovery, by wb discard, or at its deadline.](S06-09-session-lifecycle.svg)

### Session states

| State | What holds | Link drops | VM or daemon stops | Other exits |
|---|---|---|---|---|
| starting | `wb-hostd` admits the session, starts or restores the VM, and `wb-guestd` creates the worktree and applies the carry-in (S08-workspace-and-git) | ended, as a failed start | lost | running when `claude` starts. ended, as a failed start, on a refusal, a failure or the deadline |
| running | `claude` runs, and `wb` relays its terminal or its streams (S05-cli) | paused | lost | ending when `claude` exits or the client goes |
| paused | the link is down, and the guest's project processes are stopped (S13-guest-confinement, "Layer 1") | stays paused | lost | running or ending when the link returns. lost at the pause limit |
| ending | `claude` has exited or was killed, and `wb-guestd` commits WIP and pushes (S08-03-pushes, "Session end") | stays ending, and finishes again when the link returns | lost | ended when done, or as a failure at the deadline |
| lost | the session's VM, its `wb-guestd`, or `wb-hostd` stopped under it | | | ended by recovery, `wb discard`, `wb project rm`, or the deadline |
| ended | the exit status and the summary are in `wb sessions`, and the returned work stays until `wb land` or `wb discard` | | | |

`wb-hostd` logs every transition to paused, lost and ended, and each
answer it gives `wb-guestd` (S06-11-reconnect-and-recovery), with the rule that made it, in the
audit log (SEC10-audit).

### As the project user

Every operation under a project user's home
(creating and removing the worktree, the carry-in, the history link
of S06-08-projects-and-sessions, the removal of a stale
`index.lock` that a killed WIP commit left, by that fixed file name
and never by a pattern, the WIP commit and the push) runs in a
child process as that user, with the session's environment
allowlist (S09-policy-credentials-audit). `wb-guestd`, running as
root, never opens a path under a project user's home. So a project
user's `.git/config` or hooks never run as root.

### Session helper

`wb-guestd` starts each session through a small
helper that runs as the project user. The helper starts a new POSIX
session (`setsid`). In interactive use it makes the PTY its
controlling terminal, and it starts `claude` in a process group of
its own as the terminal's foreground group, as a shell does for a
job. It waits with `WUNTRACED`, so it sees `claude` stop, and reports
that, and `claude`'s exit status, to `wb-guestd`. It ignores the
`SIGHUP` it gets when the PTY's other end closes, so it can still
report `claude`'s exit after a hangup. The session's processes are
the processes in the helper's POSIX session. `wb-guestd` records
that POSIX session id in a root-owned directory on the data disk,
outside every project user's home, and uses a recorded id only for
processes whose user ID is this session's project user. A process
that leaves it with `setsid` is out of the session (S13-guest-confinement,
"Known gaps"). That job control works through this helper is not
checked yet (I135).

### Closing the terminal ends the session

When `wb` gets `SIGHUP`,
or its connection to `wb-hostd` closes for any reason (`wb` was
killed or crashed), `wb-hostd` has `wb-guestd` hang up the session.
`wb-hostd` detects this by the closed connection, never by a missed
heartbeat, so a `wb` stopped with job control keeps its session. In
interactive use `wb-guestd` closes its end of the session's PTY, and
the guest kernel sends `SIGHUP` as it does for a closed terminal.
Otherwise the helper sends `SIGHUP` to `claude`'s process group and
closes its standard input. A stopped group also gets `SIGCONT`, so
the hangup is delivered. When `claude` hasn't exited 10 seconds
later, `wb-guestd` kills the session's processes, and the project
user's other processes as S13-guest-confinement ("Layer 1") says.
Either way the session goes on to ending, as on a normal exit.
`wb claude --continue` then starts a new session at the host's
`HEAD`, which resumes the conversation ("Projects and sessions
inside a VM"). The files the old session left uncommitted are only
in its WIP commit, on its session branch.

### Ending

The session ends when `claude` exits, or after a hangup.
`wb-guestd` commits and pushes WIP (S08-03-pushes, "Session end") and reports `claude`'s exit status, an exit code or the signal
that ended it. The report is guest input (SEC11-root-gains-nothing):
`wb-hostd` accepts an exit code from 0 to 255 or a signal number
from 1 to 64. Anything else is recorded as a failure with the rule,
and `wb` exits as for a Wraith Box failure (S05-cli, "Exit status
and signals"). When `wb` is still connected, it prints the summary
and exits. `wb-guestd` keeps each finished session's report (its
exit status and whether the WIP commit and push succeeded) in its
root-owned directory until `wb-hostd` acknowledges it, and sends it
again after a reconnect. So a report lost in a dropped link still
ends the session normally, not as lost. `wb-guestd` deletes a
report when it is acknowledged, and a report or a recorded POSIX
session id when it gets "end" for that session (S06-11-reconnect-and-recovery, "Reconnect"), so
the directory holds at most the 64 sessions of one VM. When the
session's client went and no session of its project user was left,
the report also has a cleanup field, the result of the kill loop
that followed (S06-10-leftover-processes): clean, or the count of
processes left, capped at 1001.

### Returned work comes from the host

The summary's commit count
and its "(+ WIP)" mark come from `landing.git`, not from the
report. A report that claims a push that `wb-hostd`'s `receive-pack`
didn't accept for that session's branch is recorded as a failed
push, and `wb` exits with 255, with the rule logged. A report of
nothing to commit is taken as reported.

### Changes left in the guest

When a session ends at a deadline,
or its WIP commit or push failed, its changes stay in the guest
worktree. The summary and `wb sessions` then give the worktree's
path in the guest and `wb shell --project P` to reach it, and say
that `wb discard` deletes it (NFR06-explained-refusals).

### Deadlines

Each state that waits on the guest has a deadline on
the host, counted in host time awake: starting has 10 minutes from
admission, which includes the toolchain reconcile (FR07-toolchain-manifest),
and ending and recovery have 7 minutes each, which hold the 10
seconds of the hangup, the WIP commit and the 5-minute push
watchdog (S08-05-push-limits, "Bounds"). Past its deadline,
`wb-hostd` has `wb-guestd` end the session (S06-11-reconnect-and-recovery, "Reconnect") and records it
as ended with a Wraith Box failure, and `wb` exits with 255. The
worktree keeps its changes until `wb discard`. So a guest that never
reports can't hold a session, or its VM, past the deadline
(SEC13-bounded-resources). A kill of a project user ("Leftover
processes") has 7 minutes from the kill, or from the start of ending
for one that follows a client that went. Past that deadline, without
an accepted clean result, the kill counts as processes left
(S06-10-leftover-processes, "Failing closed").

### Host sleep

The VM sleeps with the host, and on wake `wb-hostd`
updates the guest clock (S06-06-devices-and-warm-start, "Time and sleep"). The session runs on as
long as `wb` still has its terminal. When the host-guest link drops
across the sleep, the session pauses and resumes as S06-11-reconnect-and-recovery (S06-11-reconnect-and-recovery, "Reconnect") says. `wb` over
SSH gets `SIGHUP` when the SSH connection drops, and that ends the
session.

### Idle suspend

The idle period of S06-06-devices-and-warm-start, "Warm start", runs only while the
VM has no session that is starting, running, paused or ending, debug
shells included. A lost or ended session doesn't count. So a VM is
never saved for idleness with such a session in it, and no restore
has a session to resume. While a kill of a project user in the VM is
pending, and after a result of processes left, the idle period
doesn't run and `wb vm suspend` is refused with the reason
(S06-10-leftover-processes). `wb vm suspend` and `wb vm stop` are
refused while the VM has a starting, running, paused or ending
session. The refusal names each one with its state, the process ID
of its `wb`, and how it ends:
close its terminal for a running or paused session, or wait for its
deadline for a starting or ending one, and `wb sessions` lists them
(NFR06-explained-refusals). A terminal left open on a session keeps
its VM running (NFR03-footprint) and holds the VM's slot
(NFR05-two-macos-vms).
Rejected: saving a VM whose sessions have been quiet, which needs a
restore on the next keystroke and turns every restore into a pause.

### Parallel sessions

Each session has its own id, worktree, client,
exit status and summary (FR05-parallel-sessions). Ending one session
doesn't touch the others, except that the project user's processes
are all killed and the user is locked when its project's last
session ends (S13-guest-confinement, "Layer 1").

**Status:** Draft
