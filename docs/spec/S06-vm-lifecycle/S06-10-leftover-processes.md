# S06-10 - Leftover Processes

**Purpose:** The results of the kill of a project user, how `wb-hostd`
accepts them, and what it does when processes are left.

## Design

### Leftover processes

The kill loop of S13-01-process-hardening ends clean, or with
a count of processes left.

#### Kill IDs and records

`wb-hostd` sends a kill of a project user in two cases:

- when it ends a project's last session in the VM;
- at the first connection, for users with no lost session. It sends
  that kill only after it has answered the reconnect list.

Each kill carries a kill ID that `wb-hostd` assigns. For each one,
`wb-guestd` writes a cleanup record to its root-owned directory, with:

- the kill ID;
- the project ID;
- the result: clean, or the count of processes left, capped at 1001.

It keeps the record until `wb-hostd` acknowledges it, and lists it at
reconnect (S06-11-reconnect-and-recovery, "Reconnect").

It holds at most one record per project user in the VM, counted apart
from the 64 session entries. A newer record replaces an older one of
the same user. The replaced kill then fails closed at its deadline.

The loop can run because a session's client went and no session of
that user is left. The result is then the cleanup field of that
session's report (S06-09-session-lifecycle, "Ending").

#### Acceptance

`wb-hostd` accepts a result only when all of these hold:

- The project ID passes the project ID format check of S05-04-naming.
- The record's kill ID is exactly one that `wb-hostd` sent for that
  project, in this VM and this VM generation. Or the report is that of
  the session whose end led to the kill.
- It arrives before the kill's deadline (S06-09-session-lifecycle, "Deadlines"), in the VM generation the kill was sent to.

Anything else is dropped, and logged with its rule.

#### Failing closed

A kill without an accepted clean result by its deadline counts as a
result of processes left. A late, missing, or dropped result never
counts as clean.

`wb-hostd` keeps its pending kills in memory, not in `state.db`. A
`wb-hostd` exit stops every VM. A pending kill then closes either way.

A pending kill whose VM generation ends in a cold stop before its
deadline is closed instead, because the guest it targeted no longer
runs. A cold stop is a stop that `wb-vmd` reports for any reason:

- guest shutdown and panic included;
- `wb vm stop`;
- a `wb-hostd` exit;
- the cold boot of an upgrade.

An in-place reboot of the guest isn't a cold stop.

A stop the guest forces is safe, because a running generation has no
saved state, and suspend is refused while a kill is pending
(S06-09-session-lifecycle, "Idle suspend").

`wb-hostd` logs each kill a cold stop closes, with its rule and the
stop's cause (SEC10-audit).

The refusals below belong to the VM generation that produced them. A
cold boot starts a new generation without them.

#### A result of processes left

For a result of processes left, `wb-hostd`:

- writes an audit event with the rule (SEC10-audit);
- tells the user in `wb status` and at the next `wb` command. Both name
  the project from `wb-hostd`'s own table, never from the guest's
  string. Both show the count capped ("more than 1000");
- refuses every new session in that VM, of any project, and learn
  mode, until the VM is cold-booted (NFR06-explained-refusals).
  Sessions that already run continue;
- never saves the VM. Idle suspend doesn't run, and `wb vm suspend` is
  refused with the reason. They also are while a kill is pending
  (S06-09-session-lifecycle, "Idle suspend");
- makes the VM's next start a cold boot, and deletes a saved state from
  before the result;
- offers `wb vm stop` followed by `wb vm start` (S05-cli). They work
  once no session runs in the VM.

Only a cold boot clears the refusals, never a suspend and restore.

#### Guest root

Guest root can fake a result or hold it back.

- A faked count, a withheld result or a late one only makes `wb-hostd`
  stricter. T11-shared-vm-grants records that cost.
- A faked clean result leaves the residual of
  S07-17-approvals-and-learning, "Leftover processes". A cold boot and
  the review of the learn list cover it.

**Status:** Draft
