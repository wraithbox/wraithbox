# C07 - Dependency Gate

**Purpose:** The test cases for the dependency gate: registry paths,
minimum age and known vulnerabilities, the Go checksum database clock,
and Homebrew bottles from `ghcr.io`.

**Verifies:** S07-12-dependency-gate, S07-10-anonymous-credentials ("Token
fallback"), B76-gosumdb-clock

## Fuzzing

The dependency gate's parsers:

- npm request paths;
- PyPI distribution file names;
- Go escaped module paths and versions, checking that the clock lookup
  key and the download URL come from the same parse;
- crates.io index lines;
- `sum.golang.org` lookup responses.

## Conformance

### Registry paths

- Install a scoped npm package (`/@scope%2fname`), and read a GitLab
  project by its encoded path (`/api/v4/projects/group%2Fproject`), and
  see both pass.
- Fetch `/@scope/name`, `/@scope%2fname` and `/@scope%2Fname`, and get
  the same gate decision for each.
- Fetch a gated registry path that fits no known form, and get a `403`
  naming the rule.
- With a test double for the Go module proxy, redirect a download
  elsewhere, and see it refused.

### Minimum age and vulnerabilities

- Download a package version younger than the minimum age, or with a
  known vulnerability, and find the too-young version missing from the
  registry metadata.
- Do the same for Go, with the checksum-database clock and
  `sum.golang.org` reached through the gate (I76).
- With `index.golang.org` unreachable before the first calibration, see
  every Go download and `@v/list` refused with `go-clock-uncalibrated`.

### The Go clock

With test doubles for `sum.golang.org` and `index.golang.org`, give the
Go clock each violation, and see it fail closed with its rule:

- a lookup reply that fails verification: the download refused with
  `go-clock-lookup-failed` and nothing cached;
- a tree head inconsistent with the last accepted one:
  `go-clock-lookup-failed` and a `go-sumdb-inconsistent` finding in `wb
  status`;
- more lookups than the VM's in-flight cap, its per-minute cap and its
  queue: the excess refused with `go-clock-lookup-rate`, and no extra
  request reaching the double;
- a double that answers slower than the deadline allows:
  `go-clock-deadline`, with the completed lookups cached;
- a download admitted behind a cold list of 2,000 versions in the same
  VM: served, not refused with `go-clock-deadline`;
- a `@v/list` of 2,001 versions: `go-list-too-long`;
- an index window whose point exceeds the tree bound: the point
  rejected with `go-clock-implausible`, the last accepted point kept,
  and the rejection with both numbers in `wb status`;
- an index that fails after a point was accepted: the point kept,
  `go-clock-stale` logged, and the point's age in `wb status`.

### The Go clock across a restart

Restart `wb-proxyd` with records in `state.db`, and see:

- a stored point equal to the current tree size isn't used, and
  requests wait for the first computation;
- a record that fails `wb-hostd`'s checks (an oversized head, a 193rd
  head, a negative point, a cutoff after the receipt time minus the
  minimum age) is logged and discarded;
- a stored head that isn't a consistent prefix of the first head
  fetched, and one larger than it that the fresh head isn't a prefix
  of, each give `go-sumdb-inconsistent`, with both heads stored and
  every Go request refused;
- a consistency proof the double won't serve gives
  `go-clock-lookup-failed` and no finding;
- a fresh head whose proof against the stored head can't be fetched
  isn't used for calibration, requests get `go-clock-uncalibrated`, and
  the stored head is checked again at the next attempt;
- a stored finding whose heads don't verify, or are consistent, is
  discarded with a `go-sumdb-finding-invalid` Detection Finding (2004);
- a head inconsistency within a run refuses every Go download, still
  does after a restart, and stops only after `wb gate reset go-clock`
  is confirmed, with a 5019 event naming the command and the finding.

### Resetting the clock

- Run `wb gate reset go-clock` without a terminal, and see it refuse
  and leave the clock as it was.
- Run it with one, and see that the restart of `wb-proxyd` it causes
  doesn't count toward the restart limit of S04-07-supervision, "Restarting a daemon", so five resets in 10 minutes leave `wb-proxyd`
  running.

### Homebrew bottles

Spec: S07-10-anonymous-credentials, "Token fallback".

- Download a Homebrew bottle from `ghcr.io` with the guest's
  `Authorization` removed and the anonymous binding injected, through
  the redirect to `pkg-containers.githubusercontent.com`.
- Send any other method to `/v2/homebrew/core/`, and see it refused.
- Send `ghcr.io/token?scope=repository:homebrew/core/jq:pull,push` with
  guest Basic credentials, and see it denied.
- With a stand-in for `ghcr.io` that refuses the fixed value, see the
  token fallback:
  - fetch only the fixed `ghcr.io/token` URL when the 401 names another
    realm;
  - refuse a bottle name outside the OCI repository-name grammar;
  - refuse requests past the per-VM rate with `anonymous-token-cap`;
  - not retry a 401 with a fresh token.
- Request `/v2/homebrew/core/x/../../other/image/...` and
  `/v2/homebrew%2Fcore/...` on `ghcr.io`, and paths with `%zz`, a
  backslash, a `%2e%2e` segment, or a segment like `wget%2F..%2Fcurl`
  or `wget%2F%2e%2e%2Fcurl`, and see all refused with nothing sent
  upstream.

**Status:** Draft
