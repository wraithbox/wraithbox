# S06-09 - Session Lifecycle

**Purpose:** A session's states from `wb claude` to its summary, how it
starts and ends, its deadlines, and when its VM may be saved.

## Design

A session runs from `wb claude` to its summary.

- `wb-hostd` keeps each session's record and state in `state.db`
  (S04-architecture, "Host state"). The record has the VM and the VM
  generation the session runs in.
- `wb-guestd` holds the session's processes. In interactive use it
  also holds its terminal device (PTY).

Closing the terminal ends the session. V1 has no detach and no `wb
attach`. A session has one client, the `wb` process that started it.

![Session states. A session starts, runs, and ends with a WIP commit and push. A start that fails or times out ends at once. A dropped host-guest link pauses a running session, which resumes or finishes when the link returns. A stopped VM or daemon, or a pause past 30 seconds, loses a session, and a lost session ends by recovery, by wb discard, or at its deadline.](S06-09-session-lifecycle.svg)

### Session states

| State | What holds | Link drops | VM or daemon stops | Other exits |
|---|---|---|---|---|
| starting | `wb-hostd` admits the session, starts or restores the VM, and `wb-guestd` creates the worktree and applies the carry-in (S08-workspace-and-git) | ended, as a failed start | lost | running when `claude` starts. ended, as a failed start, on a refusal, a failure or the deadline |
| running | `claude` runs, and `wb` relays its terminal or its streams (S05-cli) | paused | lost | ending when `claude` exits or the client goes |
| paused | the link is down, and the guest's project processes are stopped (S13-01-process-hardening) | stays paused | lost | running or ending when the link returns. lost at the pause limit |
| ending | `claude` has exited or was killed, and `wb-guestd` commits WIP and pushes (S08-03-pushes, "Session end") | stays ending, and finishes again when the link returns | lost | ended when done, or as a failure at the deadline |
| lost | the session's VM, its `wb-guestd`, or `wb-hostd` stopped under it | | | ended by recovery, `wb discard`, `wb project rm`, or the deadline |
| ended | the exit status and the summary are in `wb sessions`, and the returned work stays until `wb land` or `wb discard` | | | |

`wb-hostd` logs these in the audit log, with the rule that made each
one (SEC10-audit):

- every transition to paused, lost and ended;
- each answer it gives `wb-guestd` (S06-11-reconnect-and-recovery).

### As the project user

Every operation under a project user's home runs in a child process as
that user, with the session's environment allowlist
(S09-policy-credentials-audit). These operations are:

- creating and removing the worktree;
- the carry-in;
- the history link of S06-08-projects-and-sessions;
- the removal of a stale `index.lock` that a killed WIP commit left.
  It goes by that fixed file name, never by a pattern;
- the WIP commit and the push.

`wb-guestd`, running as root, never opens a path under a project
user's home. So a project user's `.git/config` or hooks never run as
root.

### Session helper

`wb-guestd` starts each session through a small helper that runs as
the project user. The helper:

1. starts a new POSIX session (`setsid`);
2. in interactive use, makes the PTY its controlling terminal;
3. starts `claude` in a process group of its own, as the terminal's
   foreground group, as a shell does for a job;
4. waits with `WUNTRACED`, so it sees `claude` stop;
5. reports that, and `claude`'s exit status, to `wb-guestd`.

It ignores the `SIGHUP` it gets when the PTY's other end closes. So it
can still report `claude`'s exit after a hangup.

The session's processes are the processes in the helper's POSIX
session. `wb-guestd` records that POSIX session ID in a root-owned
directory on the data disk, outside every project user's home. It uses
a recorded ID only for processes whose user ID is this session's
project user. A process that leaves the POSIX session with `setsid` is
out of the session (S13-02-filesystem-profile, "Known gaps").

### Closing the terminal ends the session

`wb-hostd` has `wb-guestd` hang up the session when:

- `wb` gets `SIGHUP`;
- `wb`'s connection to `wb-hostd` closes for any reason, as when `wb`
  was killed or crashed.

`wb-hostd` detects this by the closed connection, never by a missed
heartbeat. So a `wb` stopped with job control keeps its session.

The hangup works like this:

- In interactive use, `wb-guestd` closes its end of the session's PTY.
  The guest kernel sends `SIGHUP`, as it does for a closed terminal.
- Otherwise the helper sends `SIGHUP` to `claude`'s process group, and
  closes its standard input.
- A stopped group also gets `SIGCONT`, so the hangup is delivered.

When `claude` hasn't exited 10 seconds later, `wb-guestd` kills the
session's processes. It kills the project user's other processes as
S13-01-process-hardening says.

Either way, the session goes on to ending, as on a normal exit.

`wb claude --continue` then starts a new session at the host's `HEAD`.
It resumes the conversation (S06-08-projects-and-sessions,
"Conversation history"). The files the old session left uncommitted
are only in its WIP commit, on its session branch.

### Ending

The session ends when `claude` exits, or after a hangup. `wb-guestd`:

1. commits and pushes WIP (S08-03-pushes, "Session end");
2. reports `claude`'s exit status: an exit code, or the signal that
   ended it.

The report is guest input (SEC11-root-gains-nothing). `wb-hostd`
accepts an exit code from 0 to 255, or a signal number from 1 to 64.
Anything else is recorded as a failure with the rule. `wb` then exits
as for a Wraith Box failure (S05-02-exit-status-and-signals).

When `wb` is still connected, it prints the summary and exits.

#### Reports kept in the guest

`wb-guestd` keeps each finished session's report in its root-owned
directory, until `wb-hostd` acknowledges it. The report has the exit
status, and whether the WIP commit and push succeeded. `wb-guestd`
sends it again after a reconnect. So a report lost in a dropped link
still ends the session normally, not as lost.

`wb-guestd` deletes:

- a report, when it is acknowledged;
- a report or a recorded POSIX session ID, when it gets "end" for that
  session (S06-11-reconnect-and-recovery, "Reconnect").

So the directory holds at most the 64 sessions of one VM.

The session's client can go when no session of its project user is
left. The report then also has a cleanup field: the result of the kill
loop that followed (S06-10-leftover-processes). That is clean, or the
count of processes left, capped at 1001.

### Returned work comes from the host

The summary's commit count and its "(+ WIP)" mark come from
`landing.git`, not from the report.

- A report can claim a push that `wb-hostd`'s `receive-pack` didn't
  accept for that session's branch. It is recorded as a failed push.
  `wb` exits with 255, and the rule is logged.
- A report of nothing to commit is taken as reported.

### Changes left in the guest

A session's changes stay in the guest worktree when it ends at a
deadline, or when its WIP commit or push failed. The summary and `wb
sessions` then (NFR06-explained-refusals):

- give the worktree's path in the guest;
- give `wb shell --project P` to reach it;
- say that `wb discard` deletes it.

### Deadlines

Each state that waits on the guest has a deadline on the host,
counted in host time awake:

- starting has 10 minutes from admission. That includes the toolchain
  reconcile (FR07-toolchain-manifest);
- ending and recovery have 7 minutes each. That holds the 10 seconds
  of the hangup, the WIP commit and the 5-minute push watchdog
  (S08-05-push-limits, "Bounds").

Past its deadline, `wb-hostd`:

1. has `wb-guestd` end the session (S06-11-reconnect-and-recovery, "Reconnect");
2. records it as ended with a Wraith Box failure. `wb` exits with 255.

The worktree keeps its changes until `wb discard`. So a guest that
never reports can't hold a session, or its VM, past the deadline
(SEC13-bounded-resources).

A kill of a project user (S06-10-leftover-processes) has 7 minutes.
They count from the kill, or from the start of ending for a kill that
follows a client that went. Past that deadline, without an accepted
clean result, the kill counts as processes left
(S06-10-leftover-processes, "Failing closed").

### Host sleep

The VM sleeps with the host. On wake, `wb-hostd` updates the guest
clock (S06-06-devices-and-warm-start, "Time and sleep").

The session runs on as long as `wb` still has its terminal. When the
host-guest link drops across the sleep, the session pauses and resumes
as S06-11-reconnect-and-recovery (S06-11-reconnect-and-recovery, "Reconnect") says.

`wb` over SSH gets `SIGHUP` when the SSH connection drops, and that
ends the session.

### Idle suspend

The idle period of S06-06-devices-and-warm-start, "Warm start", runs
only while the VM has no session that is starting, running, paused or
ending, debug shells included. A lost or ended session doesn't count.
An open `wb vm admin` connection stops the idle period too
(S15-05-admin-path, "Marked").
So a VM is never saved for idleness with such a session in it, and no
restore has a session to resume.

While a kill of a project user in the VM is pending, and after a
result of processes left, the idle period doesn't run.
`wb vm suspend` is then refused with the reason
(S06-10-leftover-processes).

`wb vm suspend` and `wb vm stop` are refused while the VM has a
starting, running, paused or ending session. The refusal names each
such session with (NFR06-explained-refusals):

- its state;
- the process ID of its `wb`;
- how it ends: close its terminal for a running or paused session, or
  wait for its deadline for a starting or ending one.

`wb sessions` lists them.

A terminal left open on a session keeps its VM running
(NFR03-footprint). It also holds the VM's slot (NFR05-two-macos-vms).

### Parallel sessions

Each session has its own ID, worktree, client, exit status, and summary
(FR05-parallel-sessions). Ending one session doesn't touch the others.

The one exception is the end of its project's last session. Then all
the project user's processes are killed, and the user is locked
(S13-01-process-hardening).

## Decisions

### No detach in V1

Closing the terminal ends the session, and V1 has no `wb attach`.

- **Decided:** I46 (B46-session-lifecycle).

### Idle suspend only without live sessions

- **Rejected:** saving a VM whose sessions have been quiet. That needs
  a restore on the next keystroke, and turns every restore into a
  pause.

**Status:** Draft
