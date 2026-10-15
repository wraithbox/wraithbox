# S07-16 - Go Lookups

**Purpose:** How the dependency gate looks up a Go version's record
number in the checksum database, verifies it, caches it, and bounds the
work.

## Design

The calibration point that a record number is compared with is on
S07-09-publish-time.

### Lookup

A version is old when its record number is at or below the point.

`wb-proxyd` reads the record number from a lookup it has verified the
way `golang.org/x/mod/sumdb` does:

- the signed tree head, checked against the verifier key;
- the record's inclusion in that tree;
- the tree's consistency with the last head it accepted.

The verifier key is the go command's default key for `sum.golang.org`,
compiled into `wb-proxyd`. Policy, the guest and the network never
supply it. Only a release changes it.

A lookup can fail, time out, or fail that check. It then:

- refuses the download with the rule `go-clock-lookup-failed`;
- hides the version from a list, with the same rule in the audit
  event.

#### Young until first looked up

The gate's own lookup adds a record for a version the checksum
database doesn't have yet. So such a version is young for the minimum
age from that first request. That covers a version nobody has looked
up before, including a pseudo-version of an old commit nobody has
asked for.

A refusal says when the version becomes allowed. That date is marked
as an estimate, from its record number at the rate records were added
since the point. Under a kept stale point, the refusal also gives the
point's age.

### Inconsistent heads

Any two verified heads that aren't consistent are a finding: the
smaller tree isn't a prefix of the larger. That holds whether the
heads were fetched in this run or stored. `wb-proxyd` then:

1. logs the event `go-sumdb-inconsistent`, a Detection Finding (2004)
   that `wb status` shows;
2. from then on refuses every Go download, `@v/list` and `@latest`,
   with the rule `go-sumdb-inconsistent`;
3. sends the finding, with both heads as the proof, to `wb-hostd` as
   an accounting record. The refusal goes on after a restart.

A consistency proof that can't be fetched isn't a finding. The request
is refused with `go-clock-lookup-failed`.

#### Recovery

The operator recovers with `wb gate reset go-clock` (S05-cli).

1. `wb` prints both heads and their receipt times, and asks for
   confirmation.
2. `wb` sends the command to `wb-hostd` over local IPC. It is never
   part of a guest RPC.
3. `wb-hostd` deletes the finding and the stored heads.
4. `wb-hostd` writes a 5019 event, naming the user's command and the
   finding it deleted.
5. `wb-hostd` restarts `wb-proxyd`, which then starts from a fresh
   head. No RPC to `wb-proxyd` is added for it.

The restart resets every proxied stream of both VMs, `claude` API
streams included. It starts a new wait of up to 30 seconds for the
first point. It doesn't count toward the restart limit
(S04-07-supervision, "Restarting a daemon"), because the operator
asked for it.

The command refuses to run without a terminal, because the
confirmation has to come from a person.

### Events

| Event | Class |
|---|---|
| `go-clock-stale` | Device Config State Change (5019) |
| `go-clock-implausible` | Device Config State Change (5019) |
| `go-sumdb-inconsistent` | Detection Finding (2004) |
| `go-sumdb-finding-invalid` | Detection Finding (2004) |

A stored finding that fails verification again means `state.db` was
altered, or there is a bug.

All four belong to `wb-proxyd`, not to a VM. So the per-VM audit
budgets don't apply to them. Each is written at most once a minute
(SEC10-audit).

### Record cache

A record number never changes. So `wb-proxyd` caches it with no
expiry. It caches only records from verified lookups.

Each VM has its own cache. So one VM can't learn from a lookup's
timing which module versions another VM fetched
(SEC08-proj-isolation).

A cache holds up to 100,000 entries, and evicts the least recently
used. An evicted entry costs only a new lookup.

### Lookup limits

These limits serve SEC13-bounded-resources (S04-08-records-and-bounds,
"Host work the guest can cause").

A verified lookup takes a `/lookup/` request and the tile fetches its
proof needs. That is about two requests when the tiles aren't cached.

| Limit | Value |
|---|---|
| requests in flight per VM | 4 |
| uncached lookups per VM | 6,000 a minute |
| requests to `sum.golang.org` and `index.golang.org` in flight, across all VMs | 8 |
| per-VM queue | 2,000, the list cap ("Filtering `@v/list`") |
| timeout per upstream request | 10 seconds |

One of the 8 is kept for the timer's calibration, and the tiles it
fetches itself. Guest lookups and their tile fetches use the other 7,
never the kept one. So they can't starve calibration.

A lookup over the in-flight caps waits in the per-VM queue. A request
whose lookups would go past that queue or the per-minute cap is
refused with the rule `go-clock-lookup-rate`.

A failed lookup is retried once, after a one-second backoff. The retry
counts against the VM's budget.

### Tile cache

Verified tiles are cached per VM, like records, up to 16 MiB each. The
least recently used is evicted. A cached tile makes a lookup's proof
one request shorter.

The tiles calibration fetches go in a cache of their own of 1 MiB,
never in a VM's cache.

### Deadline

A request's lookups have a deadline of 42 seconds, plus 125 ms for:

- each uncached lookup it needs;
- each lookup already queued ahead of it in the VM.

The deadline is at most 5 minutes.

- A cold verified lookup is two upstream requests. The 42 seconds
  cover a timeout, the backoff and a second timeout for each. So one
  retry of each always fits.
- The 125 ms are 250 ms for each round of 4 requests, two requests per
  lookup.

Both counts are fixed when the request is admitted, without the
retries of the lookups queued ahead. So a degraded upstream can still
cause `go-clock-deadline`, which fails closed. The VM's requests are
served first in, first out.

Counting the queue ahead keeps a download admitted behind a cold list
from running out of time while it waits. The queue cap keeps the sum
within 5 minutes.

Past the deadline, the request is refused with the rule
`go-clock-deadline`, and its queued lookups are dropped.

- Lookups it completed stay cached. A retry then needs fewer.
- A request the client abandons releases its queue entries at once.

### Filtering `@v/list`

Filtering a list takes one lookup per listed version. A list of more
than 2,000 versions is refused with the rule `go-list-too-long`.

## Decisions

### Requests served first in, first out

- **Why:** counting the queue ahead keeps a download admitted behind a
  cold list from running out of time.
- **Rejected:** serving requests in turn. It would let two large lists
  in one VM each miss their own deadline.

**Status:** Draft
