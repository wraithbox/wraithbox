# S07-13 - Name Mapping

**Purpose:** How `wb-proxyd` keeps its own checked copy of the names
`wb-netd` gave synthetic addresses: name reports, releases, replay,
lifetime, and the second count of the wildcard budget.

## Design

Each VM's copy of the name mapping arrives over the VM's hand-off
socket, in the messages of S07-04-stream-hand-off. A stream to an
address the copy doesn't hold is refused there.

### Name reports

For each `A` answer with a synthetic address, `wb-netd` sends the name
and the address on the VM's hand-off socket before the answer leaves.
With the ordering property of S07-04-stream-hand-off ("Transport"),
the report arrives before any stream to that address. A report doesn't
hold a descriptor.

`wb-proxyd` adds the pair to its copy of the VM's mapping only when
all of these hold:

- the name is in canonical form, lowercase and without a trailing dot,
  and passes the name form rule (S07-03-dns);
- the VM's effective policy allows the name;
- the address is in the synthetic range;
- the copy doesn't hold the address for another name;
- a name allowed only by a wildcard entry fits the wildcard budget
  ("Wildcard budget, second layer").

Otherwise it drops the report. It logs it with the rule
`name-report-refused` and the check that failed: `message-form`,
`name-form`, `not-allowed`, `synthetic-range`, `address-conflict` or
`wildcard-budget`.

#### Malformed reports

A report or release that fails the message form, the name form or the
synthetic range is malformed. An honest `wb-netd` never sends one,
because `wb-hostd` checks the same entry first. For a malformed
message, `wb-proxyd` also:

1. closes the VM's hand-off socket, and every stream that came on it;
2. tells `wb-hostd` on its control channel.

`wb-hostd` ends that `wb-netd`, and counts the exit toward the restart
limit, as for a malformed accounting record
(S04-08-records-and-bounds, "Accounting and event channels").

#### Address conflicts

A report whose address the copy holds for another name means
`wb-netd` gave out an address it hadn't released. `wb-proxyd` then:

- removes the old entry too;
- closes every open stream to that address.

Streams to it are then refused until a later report is accepted.

A report that repeats an entry the copy holds, such as a replay,
doesn't change the copy.

### Releases

An entry stays in the copy until `wb-netd` releases its address.
`wb-netd` sends the release when it frees the address for another
name. That is after the TTL plus the guard interval, with no
connection to it open (S07-03-dns, "Synthetic pool").

So `wb-netd`'s clock governs. `wb-proxyd` doesn't expire entries on a
clock of its own. A stream already open keeps the name it got at
hand-off.

- A release for an address the copy doesn't hold is logged and
  ignored.
- If a release can't be sent, `wb-netd` keeps the address out of the
  pool. It sends the release again, in order, before any later report.

The copy holds at most the 131,072 addresses of the pool
(SEC13-bounded-resources).

### Replay

On each new hand-off socket, `wb-netd` first sends a name report for
every live entry of its mapping. It sends them in order, and before
any other message. A replay send that fails with `ENOBUFS` is retried
in order, so no later message overtakes it.

`wb-proxyd` checks each report as in "Name reports", with the bounds
that `wb-hostd` puts on mapping entries:

- at most one entry per address of the pool;
- the name in canonical form;
- the address in the synthetic range.

After a `wb-proxyd` restart the replay rebuilds its copy.

After a `wb-netd` restart, the new `wb-netd` holds the mapping that
`wb-hostd` kept from its accounting records
(S04-08-records-and-bounds, "Accounting and event channels", and
S04-07-supervision, "Restarting a daemon"). Its replay matches the
copy `wb-proxyd` kept.

### Lifetime

The copy belongs to the VM. `wb-proxyd` keeps it across a save and
restore, as `wb-netd`'s mapping is kept (S04-architecture). Time while
the VM is saved counts as it does for that mapping.

The copy and `wb-netd`'s mapping are both cleared only at a start that
isn't a restore. Otherwise releases alone remove entries. So a VM that
idles and is saved and restored keeps the answers its guest cached.

### Wildcard budget, second layer

`wb-netd` debits the wildcard budget before it answers (S07-03-dns).
`wb-proxyd` keeps a second count against the same budget: the distinct
names it accepted only under a wildcard entry, per VM and active
period. A replayed or repeated name isn't counted again.

Before it accepts a name that adds to the count, `wb-proxyd` writes
the name to `wb-hostd` as an accounting record
(S04-08-records-and-bounds, "Accounting and event channels"). A write
that fails, or that blocks for more than 100 ms, counts as failed. The
report is then refused with the rule `name-report-refused` and the
check `wildcard-budget`.

`wb-hostd` keeps the set per VM and active period, and gives it to a
new `wb-proxyd` at start. A `wb-proxyd` restart thus keeps the count.

Refused lookups never reach `wb-proxyd`, so its count is at most
`wb-netd`'s.

The decision behind this design, I68, is on S07-04-stream-hand-off.

**Status:** Draft
