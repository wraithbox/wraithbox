# S13-01 - Layer 1: Process Hardening (v1)

**Purpose:** How `wb-guestd` hardens every process it starts for a project
user, and how it stops and kills a project user's processes.

## Design

`wb-guestd` applies this layer to every process it starts for a project
user.

### Project users

Each project has a project user (S06-vm-lifecycle), never root. Project
users keep files apart, not network traffic (T11-shared-vm-grants).

### Limits and environment

`RLIMIT_CORE` is set to 0. The environment is built from an allowlist
(S09-policy-credentials-audit).

A session's PTY replica belongs to the session's project user, with
mode 0600, before the session helper starts. No other project user can
open it.

### Work as the project user

Every operation under a project user's home runs in a child process as
that user, with the session's environment allowlist. That includes the
worktree, the carry-in, and the WIP commit and push. `wb-guestd` runs as
root, and never opens a path there (S06-09-session-lifecycle).

### When the link drops

When the host-guest socket drops, `wb-guestd` stops every process of
every project user in the VM. It repeats that in a loop until none is
left running, so no session runs on without host session control.

The stop loop sends `SIGSTOP` to every process whose user ID is in the
project users' range, 10000 to 999,999,999. A new `wb-guestd` knows no
user yet, so the loop goes by the range. The range
leaves out root, the system accounts and `nobody`. The loop has the kill
loop's bounds, at most 10 rounds and 30 seconds ("Per-user launchd
services"). When a check still finds a process running after either
bound, `wb-guestd` logs it with the rule `project-users-stop-incomplete`
and the processes left, at most 5 of them plus a count.

A link that comes back doesn't end a running loop, which runs to its
bounds. A shell start waits until no stop loop runs, and no new loop
starts until the shell's helper runs and its POSIX session is known.
So no loop stops a shell that starts after it, and a loop that starts
after a shell sees all of it.

The loop leaves alone the processes of a user whose home a removal
empties at that moment, when both their user IDs are that user's.
Those are the removal's own children, which run after its kill loop
and while the user is locked (S06-08-projects-and-sessions, "Removing
a home").

It records which processes were already stopped, such as a `claude`
stopped with job control. When the link returns:

1. It resumes a session only when `wb-hostd` answers "resume".
2. It then sends `SIGCONT` only to the processes it stopped itself.
3. On "finish" or "end" it hangs up or kills the session
   (S06-09-session-lifecycle).

A project user's processes that it stopped, and that no resumed session
holds, stay stopped until that user's processes are killed.

A new `wb-guestd` process never takes over the sessions of an earlier
one. A VM with a session is never saved for idleness. A restore then
never has a session to resume.

### Locked users

A locked project user can't run anything new: while a user is locked,
`wb-guestd` doesn't start a process as it, except the child that empties its
home in a removal (S06-08-projects-and-sessions, "Removing a home").

The lock is in `wb-guestd`'s memory only. Every project user starts
locked, and only `UnlockUser`, for a session start, unlocks one.
`UnlockUser` refuses a user `wb-guestd` hasn't made or checked since it
started, and one whose kill or removal runs. A kill and a removal lock
the user again. A kill of a user whose removal runs is refused, because
the removal runs its own kill loop. So a new `wb-guestd` process starts with every user
locked, and fails closed without stored state.

### When a session's client goes

A session's client goes when its terminal closes or `wb` is killed.
Then:

1. `wb-guestd` hangs up `claude`.
2. When `claude` hasn't exited 10 seconds later, `wb-guestd` sends
   `SIGKILL` to every process in the session's POSIX session
   (S06-09-session-lifecycle, "Session helper"). It sends it only to
   those whose effective and real user IDs are both the session's
   project user. It logs any other process in that POSIX session, and
   leaves it.
3. When the project user has no other session in the VM, a lost one
   included, it then kills every process of that user, in a loop until
   none remain.
4. Otherwise it logs each process of that user that is in no session's
   POSIX session, with the rule, and leaves it
   (S13-02-filesystem-profile, "Known gaps").

### When a project's last session ends

`wb-hostd` ends a project's last session in the VM, debug shells
included. `wb-guestd` then:

1. locks that project user, so nothing new starts as it;
2. kills its processes in a loop until none remain.

A lost session counts as a session here until recovery, `wb discard`,
`wb project rm` or its deadline ends it
(S06-11-reconnect-and-recovery, "Lost"). So recovery can still commit
and push as that user.

Until then the user's remaining processes stay stopped:

- `wb-guestd` stops them at the link drop.
- A new `wb-guestd` process stops every project user's processes when it
  starts, before it serves the host.
- On its first connection, `wb-hostd` names the project users that have
  a lost session in the VM. `wb-guestd` kills the stopped processes of
  every other project user, in a loop until none remain.

A stopped VM leaves none.

This holds against a process without guest root only. No process is
meant to keep running after its project leaves the VM's effective
policy. None is meant to keep running during another project's
learn-mode session either (S07-17-approvals-and-learning).

### Per-user launchd services

launchd starts a project user's per-user agents and services, such as a
simulator's, in that user's launchd domain (`user/<uid>`). They run
outside every session, where killing the session's processes doesn't
reach them.

So `wb-guestd` repeats the kill loop and `launchctl bootout user/<uid>`
as one loop, until a check after the `bootout` finds no process of the
user. It does so wherever it kills every process of a project user:

- at the end of a project's last session;
- in the kill at the first connection, of the users with no lost
  session;
- when a user's last client goes and no session of that user is left.

The loop is bounded: at most 10 rounds and 30 seconds. When a check
still finds a process of the user after either bound, `wb-guestd`:

1. logs the failure with its rule and the processes left, at most 5 of
   them plus a count (SEC10-audit, SEC13-bounded-resources). It escapes
   and cuts each name as S04-08-records-and-bounds ("Version skew")
   does with guest strings;
2. leaves the user locked;
3. reports the result to `wb-hostd` (S06-10-leftover-processes).

It never boots out a domain where it only stops processes, such as at
the link drop. A resumed session then keeps its services.

`wb-guestd` never runs `launchctl print` or `launchctl asuser` against a
locked project user.

### Known gap: cron and at

A project user's `crontab` and `at` jobs stay after the kill loop and a
removal. `cron` and `atrun` run as root and start them as the user,
outside its launchd domain, so `bootout` doesn't reach them, and neither the disabled
account nor its shell stops them. Closing this in the image and in the
kill loop is I442.

**Status:** Draft
