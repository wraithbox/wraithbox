# S13-01 - Layer 1: Process Hardening (v1)

**Purpose:** How `wb-guestd` hardens every process it starts for a project
user, and how it stops and kills a project user's processes.

## Design

Applied by `wb-guestd` to every process it starts for a project user:

### Project users

a project user per project (S06-vm-lifecycle), never root. Project
users keep files apart, not network traffic (T11-shared-vm-grants);

### Limits and environment

`RLIMIT_CORE` set to 0, and an environment built from an allowlist
(S09-policy-credentials-audit);

### Work as the project user

every operation under a project user's home, the worktree, the
carry-in and the WIP commit and push included, runs in a child
process as that user with the session's environment allowlist.
`wb-guestd`, running as root, never opens a path there
(S06-09-session-lifecycle);

### When the link drops

when the host-guest socket drops, `wb-guestd` stops every process of
every project user in the VM, in a loop until none is left running,
so no session runs on without host session control. It records
which processes were already stopped, such as a `claude` stopped
with job control. When the link returns, it resumes a session only
when `wb-hostd` answers "resume", and then sends `SIGCONT` only to
the processes it stopped itself. On "finish" or "end" it hangs up or
kills the session (S06-09-session-lifecycle). A
project user's processes that it stopped and that no resumed
session holds stay stopped until that user's processes are killed.
A new `wb-guestd` process never takes over the sessions of an
earlier one. A VM with a session is never saved for idleness. A
restore then never has a session to resume;

### When a session's client goes

when a session's client goes (its terminal closed, or `wb` was
killed), `wb-guestd` hangs up `claude`. When it hasn't exited 10
seconds later, `wb-guestd` sends `SIGKILL` to every process in the
session's POSIX session (S06-09-session-lifecycle, "Session helper"), and
only to those whose user ID is the session's project user. When
the project user has no other session in the VM, a lost one
included, it then kills every
process of that user, in a loop until none remain. Otherwise it logs
each process of that user that is in no session's POSIX session,
with the rule, and leaves it (S13-02-filesystem-profile, "Known gaps");

### When a project's last session ends

when `wb-hostd` ends a project's last session in the VM, debug
shells included, `wb-guestd` locks that project user, so nothing new
starts as it, and kills its processes in a loop until none remain.
A lost session counts here until recovery, `wb discard`,
`wb project rm` or its deadline ends it (S06-11-reconnect-and-recovery, "Lost"), so recovery can still commit and push as that user. Until
then the user's remaining processes stay stopped: `wb-guestd` stops
them at the link drop, and a new `wb-guestd` process stops every
project user's processes when it starts, before it serves the host.
On its first connection `wb-hostd` names the project users that have
a lost session in the VM, and `wb-guestd` kills the stopped
processes of every other project user, in a loop until none remain.
A stopped VM leaves none.
This holds against a process without guest root only. None is meant
to keep running after its project leaves the VM's
effective policy or during another project's learn-mode session (S07-11-approvals-and-learning);

### Per-user launchd services

launchd starts a project user's per-user agents and services, such
as a simulator's, in that user's launchd domain (`user/<uid>`),
outside every session, where killing the session's processes
doesn't reach them. So wherever `wb-guestd` kills every process of a
project user (at the end of a project's last session, in the kill at
the first connection of the users with no lost session, and when a
user's last client goes and no session of that user is left), the
kill loop and `launchctl bootout user/<uid>` repeat as one loop
until a check after the `bootout` finds no process of the user. The
loop is bounded: at most 10 rounds and 30 seconds. When a check
still finds a process of the user after either bound, `wb-guestd`
logs the failure with its rule and the processes left, at most 5
of them plus a count, each name escaped and cut as S04-08-records-and-bounds ("Version skew") does with guest strings (SEC10-audit,
SEC13-bounded-resources), leaves the user locked, and
reports the result to `wb-hostd` (S06-10-leftover-processes). It
never boots out a domain where it only stops processes, such as at
the link drop, so a resumed session keeps its services. `wb-guestd`
never runs `launchctl print` or `launchctl asuser` against a locked
project user.

**Status:** Draft
