# S07-09 - Publish Time

**Purpose:** Where the dependency gate gets each version's publish time, and how
it builds a clock for Go modules from the checksum database.

## Design

### Publish time

From the registry, fetched by `wb-proxyd`: npm `time`
from the full packument (the abbreviated one has none), PyPI
`upload-time` per file (PEP 700, so a file added to an old release
later is young on its own), and crates.io `pubtime` from the sparse
index. For Go, the clock is the version's record number in the
checksum database `sum.golang.org`, compared with a calibration point:
the record number at the cutoff, the minimum age ago. The `.info` Time
is the commit time, which the module's author sets, and is never used.
The clock assumes that the checksum database adds a version's record
when the module proxy first serves that version, as it did for all 320
versions sampled in X21-dep-gate-registries (I76).

### Calibration point

The lowest record number of the 20 latest
versions that `index.golang.org` shows as first seen at or before 1
minute before the cutoff. `wb-proxyd` reads the index from 10
minutes before the cutoff and pages forward until it reaches that
time. A window without an entry widens backwards in 10-minute
steps up to an hour, never forwards. If that hour has no entry
either, the computation fails. One version in the window whose
record was added when it was first seen puts the point before the
cutoff, and a record added late only raises a number the minimum
ignores. So each point is at or below the true record number at
its own cutoff, which is at or below the true record number at any
later cutoff.

### Tree bound

`wb-proxyd` fetches and verifies the signed tree head
(`/latest`) at each computation, and keeps the heads it verified in
the last 8 days, one an hour. A fresh point may not exceed the size
of the earliest of those heads fetched at or after the point's
cutoff. Records are only appended, so a correct point never
exceeds it. A point over the bound is rejected: `wb-proxyd` logs
the event `go-clock-implausible` with the point and the bound, and
the computation counts as failed ("Refresh"). The bound's slack is
the gap from the cutoff to that head, under an hour
once the heads cover the last 8 days, and can be days on a host
that was asleep or off. On a new install the bound is the current
head, which only stops a point past the end of the log. New records
only raise a head, so a burst of new records that a guest causes,
through the gate or through `/lookup/` directly, can't trip it,
while a bound on the rate of growth could.

### Refresh

`wb-proxyd` computes the point on its own timer, at
start and then every hour, one computation at a time, and never on
a guest request. After a failed computation, a rejected point
included, it retries every minute and keeps the last point it
computed and accepted in this run, which can only refuse more by
the argument above. While it keeps a point past its hour, it logs
the event `go-clock-stale` with the point's age. It recovers at the
first computation that passes. `wb status` shows the point's age at
all times, and the last rejected point with the bound it exceeded.

### Persisted state

After each accepted computation, `wb-proxyd`
sends `wb-hostd` accounting records (S04-08-records-and-bounds, "Accounting and event channels"): one for the accepted point, with its number
and cutoff, and one for each new head. `wb-hostd` owns `state.db`
and its caches, and `wb-proxyd` doesn't write files, so `wb-hostd`
stores them there (S04-architecture, "Host state"). It stamps each
record with its own receipt time, never a time from `wb-proxyd`.
It checks each record the way it checks mapping entries
(S04-08-records-and-bounds, "Mapping entries are checked"): a head is a
well-formed signed note of at most 1 KiB, at most 192 heads are
kept, and a point is a non-negative integer whose cutoff is at or
before the receipt time minus the minimum age. A record that fails
a check is logged with its rule and discarded. A failed accounting
write has no effect on the gate. At start `wb-hostd` passes the
stored point and heads back.
- The stored point is for display in `wb status` only. The gate
  decides only on numbers `wb-proxyd` verified or computed itself
  in this run, and after a restart it has to compute a point
  first ("Before the first point").
- `wb-proxyd` checks each stored head's signature again, and drops
  a head with a receipt time in the future. It then checks each
  stored head against the first head it fetches, in whichever
  direction applies: the smaller tree must be a consistent prefix
  of the larger, and it keeps the larger. A stored head that fails
  the check is a finding, the same as one within a run ("Lookup").
- The fresh head isn't accepted, and calibration doesn't use it,
  until that proof is done. While the proof can't be fetched, the
  computation fails: requests get `go-clock-uncalibrated` before
  the first point, or `go-clock-stale` applies within a run. The
  stored head is kept and checked again at the next attempt.
- A stored finding is checked at start too. `wb-proxyd` verifies its
  two heads again and checks the contradiction itself. A finding
  whose heads don't verify, or turn out consistent, is logged with
  the rule `go-sumdb-finding-invalid` and discarded.
- Across a restart, the tree bound is only as tight as the heads
  and times `wb-hostd` returns, and detection of a fork rests on
  `wb-hostd` keeping the heads and any finding. `wb-hostd` can
  withhold them, but it can't forge a contradiction, because both
  heads of a finding are signed with the verifier key. Within a run, `wb-proxyd` adds the
  heads it fetches itself, so from the second computation on the
  bound also rests on its own heads.
- A missing or unreadable record means no stored point and no
  stored heads, and `wb-hostd` logs an audit event saying so.

### Before the first point

After each start, a Go download,
`@v/list` or `@latest` waits up to 30 seconds for the first
computation. A waiting request holds its connection, which counts
against the VM's cap on connections in flight (S07-02-packet-path, "Per-VM caps"). This
wait doesn't count toward the request's lookup deadline ("Lookup
limits"). If there is no point after that, or the first computation
fails, the request is refused with the rule `go-clock-uncalibrated`
(fail closed).

### Lookup

A version is old when its record number is at or below
the point. `wb-proxyd` reads the record number from a lookup it has
verified the way `golang.org/x/mod/sumdb` does: the signed tree
head checked against the verifier key, the record's inclusion in
that tree, and the tree's consistency with the last head it
accepted. The verifier key is the go command's default key for
`sum.golang.org`, compiled into `wb-proxyd`. Policy, the guest and
the network never supply it, and only a release changes it. A lookup that fails, times out, or fails that check
refuses the download with the rule `go-clock-lookup-failed`, and
hides the version from a list with the same rule in the audit
event. Any two verified heads that aren't consistent, whether
fetched in this run or stored, are a finding: the smaller tree
isn't a prefix of the larger. `wb-proxyd` logs the event
`go-sumdb-inconsistent`, a Detection Finding (2004) that `wb status`
shows, and from then on refuses every Go download, `@v/list` and
`@latest` with the rule `go-sumdb-inconsistent`. It sends the
finding, with both heads as the proof, to `wb-hostd` as an
accounting record, and the refusal goes on after a restart. A
consistency proof that can't be fetched isn't a finding: the
request is refused with `go-clock-lookup-failed`. The operator
recovers with `wb gate reset go-clock` (S05-cli). It prints both
heads and their receipt times and asks for confirmation. `wb` sends
it to `wb-hostd` over local IPC, and it is never part of a guest RPC.
`wb-hostd` deletes the finding and the stored heads, writes a 5019
event naming the user's command and the finding it deleted, and
restarts `wb-proxyd`, which then starts from a fresh head. No RPC to
`wb-proxyd` is added for it. The restart resets every proxied
stream of both VMs, `claude` API streams included, and starts a new
wait of up to 30 seconds for the first point. It doesn't count
toward the restart limit (S04-07-supervision, "Restarting a daemon"),
because the operator asked for it. The command refuses to run
without a terminal, because the confirmation has to come from a
person. The gate's own lookup adds a record
for a version the checksum database doesn't have yet, so such a
version is young for the minimum age from that first request. That
covers a version nobody has looked up before, including a
pseudo-version of an old commit nobody has asked for. A refusal
says when the version becomes allowed, marked as an estimate from
its record number at the rate records were added since the point,
and under a kept stale point it also gives the point's age.

### Events

`go-clock-stale` and `go-clock-implausible` are Device
Config State Change (5019) events. `go-sumdb-inconsistent` and
`go-sumdb-finding-invalid` are Detection Findings (2004): a stored
finding that fails verification again means `state.db` was altered
or there is a bug. All four belong to `wb-proxyd`, not to a VM,
so the per-VM audit budgets don't apply to them, and each is written at
most once a minute (SEC10-audit).

### Record cache

A record number never changes, so `wb-proxyd`
caches it with no expiry. It caches only records from verified
lookups. Each VM has its own cache, so one VM can't learn from a
lookup's timing which module versions another VM fetched
(SEC08-proj-isolation). A cache holds up to 100,000 entries and
evicts the least recently used. An evicted entry costs only a new
lookup.

### Lookup limits

(SEC13-bounded-resources, S04-08-records-and-bounds, "Host work the guest can cause"). A verified lookup takes a `/lookup/`
request and the tile fetches its proof needs, about two requests
when the tiles aren't cached. Each VM has at most 4 such requests
in flight and 6,000 uncached lookups a minute. `wb-proxyd` has at
most 8 requests to `sum.golang.org` and `index.golang.org` in
flight across all VMs. One of the 8 is kept for the timer's
calibration and the tiles it fetches itself. Guest lookups and
their tile fetches use the other 7 and never the kept one, so they
can't starve calibration. A lookup over the in-flight caps waits in
a per-VM queue of at most 2,000, the list cap below. A request
whose lookups would go past that queue or the per-minute cap is
refused with the rule `go-clock-lookup-rate`. A failed lookup is
retried once after a one-second backoff, and the retry counts
against the VM's budget. Each request upstream times out after 10
seconds.

### Tile cache

Verified tiles are cached per VM, like records, up to
16 MiB each, and the least recently used is evicted. A cached tile
makes a lookup's proof one request shorter. The tiles calibration
fetches go in a cache of their own of 1 MiB, never in a VM's cache.

### Deadline

A request's lookups have a deadline of 42 seconds plus
125 ms for each uncached lookup it needs and each lookup already
queued ahead of it in the VM, at most 5 minutes. A cold verified
lookup is two upstream requests, and the 42 seconds cover a
timeout, the backoff and a second timeout for each, so one retry
of each always fits. The 125 ms are 250 ms for each round of 4
requests, two requests per lookup. Both counts are fixed when the
request is admitted, without the retries of the lookups queued
ahead, so a degraded upstream can still cause `go-clock-deadline`,
which fails closed, and the VM's requests are served first in,
first out. Counting the queue ahead keeps a download admitted
behind a cold list from running out of time while it waits. The
queue cap below keeps the sum within 5 minutes. Serving requests in
turn instead would let two large lists in one VM each miss their own
deadline. Past the deadline the request
is refused with the rule `go-clock-deadline` and its queued lookups
are dropped. Lookups it completed stay cached, and a retry needs
fewer. A request the client abandons releases its queue entries at
once.

### Filtering `@v/list`

One lookup per listed version. A list of
more than 2,000 versions is refused with the rule `go-list-too-long`.

**Status:** Draft
