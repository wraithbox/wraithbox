# S06-10 - Leftover Processes

**Purpose:** The results of the kill of a project user, how `wb-hostd` accepts
them, and what it does when processes are left.

## Design

### Leftover processes

The kill loop of S13-guest-confinement
("Layer 1") ends clean or with a count of processes left.

#### Kill IDs and records

Each kill of a project user that
`wb-hostd` sends (when it ends a project's last session in the VM,
and the kill of users with no lost session at the first
connection, which it sends only after it has answered the
reconnect list) carries a kill ID that `wb-hostd` assigns. For each
one, `wb-guestd` writes a cleanup record to its root-owned
directory: the kill ID, the project ID, and the result, clean or
the count of processes left, capped at 1001. It keeps the record
until `wb-hostd` acknowledges it, and lists it at reconnect
(S06-11-reconnect-and-recovery, "Reconnect"). It holds at most one record per project user in the
VM, counted apart from the 64 session entries: a newer record
replaces an older one of the same user, and the replaced kill then
fails closed at its deadline. Where the loop runs
because a session's client went and no session of that user is
left, the result is the cleanup field of that session's report
(S06-09-session-lifecycle, "Ending").

#### Acceptance

`wb-hostd` accepts a result only when the project ID
passes the project ID format check of S05-cli ("Naming"), and the
record's kill ID is exactly one that `wb-hostd` sent for that
project in this VM and this VM generation, or the report is that
of the session whose end led to the kill. It must arrive before
the kill's deadline (S06-09-session-lifecycle, "Deadlines"), in the VM generation the kill
was sent to. Anything else is dropped and logged with its rule.

#### Failing closed

A kill without an accepted clean result by its
deadline counts as a result of processes left. A late, missing or
dropped result never counts as clean. `wb-hostd` keeps its pending
kills in memory, not in `state.db`. A `wb-hostd` exit stops every
VM, so a pending kill closes either way. A pending kill whose VM
generation ends in a cold stop before its deadline is closed
instead, because the guest it targeted no longer runs. A cold stop
is a stop that `wb-vmd` reports for any reason, guest shutdown and
panic included, `wb vm stop`, a `wb-hostd` exit, and the cold boot
of an upgrade. An in-place reboot of the guest is not a cold stop.
A stop the guest forces is safe, because a running generation has
no saved state and suspend is refused while a kill is pending
(S06-09-session-lifecycle, "Idle suspend"). `wb-hostd` logs each kill a cold stop closes,
with its rule and the stop's cause (SEC10-audit). The refusals
below belong to the VM generation that produced them, and a cold
boot starts a new generation without them.

#### A result of processes left

For a result of processes left, `wb-hostd`:
- writes an audit event with the rule (SEC10-audit) and tells the
  user in `wb status` and at the next `wb` command. Both name the
  project from `wb-hostd`'s own table, never from the guest's
  string, and show the count capped ("more than 1000");
- refuses every new session in that VM, of any project, and learn
  mode, until the VM is cold-booted (NFR06-explained-refusals).
  Sessions that already run continue;
- never saves the VM: idle suspend doesn't run, and `wb vm suspend`
  is refused with the reason, as they also are while a kill is
  pending (S06-09-session-lifecycle, "Idle suspend"). Its next start is a cold boot, and a
  saved state from before the result is deleted. Only a cold boot
  clears the refusals, never a suspend and restore;
- offers `wb vm stop` followed by `wb vm start` (S05-cli), which
  work once no session runs in the VM.

Guest root can fake a result or hold it back. A faked count, a
withheld result or a late one only makes `wb-hostd` stricter, a
cost T11-shared-vm-grants records. A faked clean result leaves the
residual of S07-11-approvals-and-learning, "Leftover processes", which a cold
boot and the review of the learn list cover.

**Status:** Draft
