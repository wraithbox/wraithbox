# S07-13 - Publish Time

**Purpose:** Where the dependency gate gets each version's publish
time, and how it builds a clock for Go modules from the checksum
database.

## Design

How the gate looks up a Go version against that clock is on
S07-14-go-lookups.

### Publish time

`wb-proxyd` fetches the publish time from the registry:

| Registry | Publish time |
|---|---|
| npm | `time` from the full packument. The abbreviated one has none. |
| PyPI | `upload-time` per file (PEP 700). So a file added to an old release later is young on its own. |
| crates.io | `pubtime` from the sparse index |
| Go | the version's record number in the checksum database `sum.golang.org`, compared with a calibration point |

The calibration point is the record number at the cutoff, the minimum
age ago.

The Go `.info` Time is the commit time, which the module's author
sets. It is never used.

The clock assumes that the checksum database adds a version's record
when the module proxy first serves that version. It did so for all 320
versions sampled in X21-dep-gate-registries (I76).

### Calibration point

The point is the lowest record number of the 20 latest versions that
`index.golang.org` shows as first seen at or before 1 minute before the
cutoff.

1. `wb-proxyd` reads the index from 10 minutes before the cutoff.
2. It pages forward until it reaches that time.
3. A window without an entry widens backwards in 10-minute steps, up
   to an hour, never forwards.
4. If that hour has no entry either, the computation fails.

One version in the window whose record was added when it was first
seen puts the point before the cutoff. A record added late only raises
a number the minimum ignores.

So each point is at or below the true record number at its own
cutoff. That is at or below the true record number at any later
cutoff.

### Tree bound

`wb-proxyd` fetches and verifies the signed tree head (`/latest`) at
each computation. It keeps the heads it verified in the last 8 days,
one an hour.

A fresh point may not exceed the size of the earliest of those heads
fetched at or after the point's cutoff. Records are only appended, so
a correct point never exceeds it.

A point over the bound is rejected:

- `wb-proxyd` logs the event `go-clock-implausible`, with the point
  and the bound;
- the computation counts as failed ("Refresh").

The bound's slack is the gap from the cutoff to that head. It is under
an hour once the heads cover the last 8 days. It can be days on a host
that was asleep or off. On a new install the bound is the current
head, which only stops a point past the end of the log.

New records only raise a head. So a burst of new records that a guest
causes can't trip the bound, through the gate or through `/lookup/`
directly. A bound on the rate of growth could.

### Refresh

`wb-proxyd` computes the point on its own timer:

- at start, and then every hour;
- one computation at a time;
- never on a guest request.

After a failed computation, a rejected point included, it retries
every minute. It keeps the last point it computed and accepted in this
run. That point can only refuse more, by the argument of "Calibration
point".

While it keeps a point past its hour, it logs the event
`go-clock-stale` with the point's age. It recovers at the first
computation that passes.

`wb status` shows the point's age at all times. It also shows the last
rejected point, with the bound it exceeded.

### Persisted state

After each accepted computation, `wb-proxyd` sends `wb-hostd`
accounting records (S04-08-records-and-bounds, "Accounting and event channels"):

- one for the accepted point, with its number and cutoff;
- one for each new head.

`wb-hostd` owns `state.db` and its caches, and `wb-proxyd` doesn't
write files. So `wb-hostd` stores them there (S04-architecture, "Host state"). It stamps each record with its own receipt time, never a time
from `wb-proxyd`.

It checks each record the way it checks mapping entries
(S04-08-records-and-bounds, "Mapping entries are checked"):

- a head is a well-formed signed note of at most 1 KiB;
- at most 192 heads are kept;
- a point is a non-negative integer whose cutoff is at or before the
  receipt time minus the minimum age.

A record that fails a check is logged with its rule, and discarded. A
failed accounting write has no effect on the gate.

A missing or unreadable record means no stored point and no stored
heads. `wb-hostd` logs an audit event saying so.

#### At start

At start `wb-hostd` passes the stored point and heads back.

- **The stored point** is for display in `wb status` only. The gate
  decides only on numbers `wb-proxyd` verified or computed itself in
  this run. After a restart it has to compute a point first ("Before
  the first point").
- **Stored heads.** `wb-proxyd` checks each stored head's signature
  again. It drops a head with a receipt time in the future. It then
  checks each stored head against the first head it fetches, in
  whichever direction applies. The smaller tree must be a consistent
  prefix of the larger, and it keeps the larger. A stored head that
  fails the check is a finding, the same as one within a run
  (S07-14-go-lookups, "Lookup").
- **The fresh head** isn't accepted, and calibration doesn't use it,
  until that proof is done. While the proof can't be fetched, the
  computation fails. Requests get `go-clock-uncalibrated` before the
  first point, or `go-clock-stale` applies within a run. The stored
  head is kept, and checked again at the next attempt.
- **A stored finding** is checked at start too. `wb-proxyd` verifies
  its two heads again, and checks the contradiction itself. A finding
  whose heads don't verify, or turn out consistent, is logged with the
  rule `go-sumdb-finding-invalid`, and discarded.

#### What rests on `wb-hostd`

Across a restart, the tree bound is only as tight as the heads and
times `wb-hostd` returns. Detection of a fork rests on `wb-hostd`
keeping the heads and any finding.

- `wb-hostd` can withhold them.
- It can't forge a contradiction, because both heads of a finding are
  signed with the verifier key.

Within a run, `wb-proxyd` adds the heads it fetches itself. So from
the second computation on, the bound also rests on its own heads.

### Before the first point

After each start, a Go download, `@v/list` or `@latest` waits up to 30
seconds for the first computation.

- A waiting request holds its connection. That counts against the
  VM's cap on connections in flight (S07-02-packet-path, "Per-VM caps").
- This wait doesn't count toward the request's lookup deadline
  (S07-14-go-lookups, "Lookup limits").

If there is no point after that, or the first computation fails, the
request is refused with the rule `go-clock-uncalibrated` (fail
closed).

## Decisions

### The checksum database as the Go clock

For Go, the publish time is the version's record number in
`sum.golang.org`, not the `.info` Time.

- **Why:** the `.info` Time is the commit time, which the module's
  author sets.

### A bound on the tree size, not on its growth

A fresh point may not exceed the earliest head fetched after its
cutoff.

- **Why:** new records only raise a head, so a burst of new records
  that a guest causes can't trip the bound.
- **Rejected:** a bound on the rate of growth, which such a burst
  could trip.

**Status:** Draft
