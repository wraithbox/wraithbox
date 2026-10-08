# X21 - Dependency gate on real registries

Brief: B32-dep-gate-registries, and B76-gosumdb-clock for the Go clock

**Purpose:** The result of the spike for I32. Can `wb-proxyd` tell the
package, version and publish time of a registry download, and which way
of applying a minimum age lets real installs still work?

## For review

- **Decides:** how the dependency gate applies the minimum age. It
  removes too-young versions from registry metadata, and a resolver then
  picks an older version. It still refuses a too-young download. It also
  decides where each registry's publish time comes from, and that
  Homebrew bottles are not age-gated.
- **You are approving:** the rewritten "Dependency gate" section of
  S07-egress-gateway. On 10 real projects with a 7-day minimum age,
  fresh installs go from 10 of 10 failing (refuse the download) to 1 of
  10 (filter the metadata). Lockfile installs that pin a version younger
  than 7 days still fail (2 of 10), with a message that says when the
  version becomes allowed. Two residuals, accepted by merging:
  T07-ungated-sources (dependencies fetched from git hosts, archive URLs
  or mirrors skip the gate) and T08-homebrew-ungated (young Homebrew
  bottles aren't refused).
- **Controls touched:** SEC07-dep-gate is kept: every download of a
  too-young version from a gated registry is still refused, and the
  metadata filter comes in front of that refusal. SEC07-dep-gate lists
  npm, PyPI, Go modules and crates, not Homebrew. SEC05-default-deny is
  kept: the Go redirect is followed only to its storage host, and
  `ghcr.io` is limited to `homebrew/core`. SEC13-bounded-resources:
  held-back bodies and gated downloads in flight are bounded.
  SEC10-audit: each filtered metadata response is an audit event.
- **Assumed:** that the registries keep publishing the fields measured
  here: npm `time`, PyPI `upload-time`, crates.io `pubtime`, and the
  Go checksum database's record numbers. I76 measured the Go clock end
  to end ("The Go clock end to end"), and B76-gosumdb-clock puts its
  calibration rules up for review.
- **Open decisions:** each has a recommendation in
  B32-dep-gate-registries, and the sixth in B76-gosumdb-clock.
  1. The vulnerability threshold. At the specified default (HIGH), OSV
     data would refuse the lockfile installs of 8 of the 10 projects.
     This result doesn't change that default, and I73 asks the
     maintainer to decide.
  2. The one-hour OSV cache. A malicious-package report published
     within the hour can be missed. Recommended: accept, with the cache
     keyed on the threshold and only answers under an hour old used
     during an OSV outage.
  3. Clients that read other metadata. Recommended: refuse a PyPI
     client that doesn't accept the JSON simple index, filter npm
     `/-/package/<name>/dist-tags`, and filter the young `releases` of
     the PyPI JSON API (`/pypi/<name>/json`, used by Poetry), refusing
     it when its `info` describes a young release.
  4. Private registries. The gate fetches publish times before
     credentials are injected, so every package on a private registry
     fails closed. Recommended: leave that as is for V1 and decide how
     the gate authenticates when the first private registry binding is
     specified.
  5. Partial mitigations for T07-ungated-sources. Recommended: deny
     archive downloads in the git hosting profile, and flag a host that
     mirrors a gated registry in the approval risk check.
  6. The Go calibration point during a long outage of
     `index.golang.org` (I76). Recommended: keep the last point, which only
     refuses more, and refuse every Go request only when there has
     never been one. B76-gosumdb-clock has the options, and the values
     of the new lookup limits, which the spike supports only in part.
- **Brief:** B32-dep-gate-registries, and B76-gosumdb-clock for I76

## Question

From X00-index: can `wb-proxyd` map a download to package, version and
publish time for npm, PyPI, the Go module proxy, crates.io and
Homebrew? Does removing too-young versions from metadata let resolvers
pick an older version, where refusing the download fails the whole
install? Measure failure rates with a 7-day minimum age on real
projects, and the time OSV lookups add to an `npm ci` with a large
lockfile.

## Answer

**Yes, with conditions.** Filter the metadata and keep refusing the
download. Per registry:

| Registry | Download to package and version | Publish time | Metadata to filter | Answer |
|---|---|---|---|---|
| npm | tarball path `<name>/-/<name>-<version>.tgz` | `time` in the full packument; the abbreviated one that clients ask for has none | packument `versions` and `dist-tags` | yes |
| PyPI | the file the index listed, else the file name checked against the JSON API | `upload-time` per file in the JSON simple index (PEP 700) | JSON simple index `files` and `versions` | yes, per file |
| Go module proxy | `<module>/@v/<version>.zip` | not the `.info` Time, which is the commit time; the record number in `sum.golang.org` instead | `@v/list` and `@latest` | yes, with the checksum database as the clock, measured end to end (I76) |
| crates.io | `/crates/<name>/<version>/download` or `<name>-<version>.crate` | `pubtime` on each sparse index line | sparse index lines | yes |
| Homebrew | blob digest, through the bottle manifest | `org.opencontainers.image.created` on the manifest | none: one version per formula, and the metadata is signed | no, for the age gate |

Refusing downloads fails every fresh install, because every one of the
10 projects resolves at least one dependency younger than 7 days.
Filtering lets 9 of 10 resolve older versions. The tenth asks for a
minimum version that is itself younger than 7 days, and no strategy can
satisfy that.

## Measurements

The spike ran on 2026-10-04 against the public registries, with a
throwaway inspecting proxy on localhost and a CA trusted only by the
installing process. Each project is pinned to a commit.

### Installs with a 7-day minimum age

Fresh install: the lockfile removed (npm, uv) or the pins stripped
(pip). Lockfile install: `npm ci`, `uv sync --frozen`, or
`pip install -r` of the pinned file. "Young" is the number of distinct
package versions downloaded, out of all of them, that were younger than
7 days with the gate off.

| Project | Tool | Fresh: young | Fresh: refuse / filter | Lockfile: young | Lockfile: refuse / filter |
|---|---|---|---|---|---|
| axios | npm | 20 of 616 | fail / pass | 0 of 616 | pass / pass |
| open-webui | npm | 75 of 1082 | fail / pass | 0 of 1077 | pass / pass |
| actions/toolkit | npm | 8 of 977 | fail / pass | 0 of 966 | pass / pass |
| datasette | npm | 1 of 46 | fail / pass | 0 of 47 | pass / pass |
| undici | npm | 19 of 632 | fail / pass | 0 of 633 | pass / pass |
| flask | uv | 18 of 78 | fail / pass | 0 of 78 | pass / pass |
| fastapi | uv | 32 of 190 | fail / pass | 2 of 190 | fail / fail |
| starlette | uv | 9 of 79 | fail / pass | 0 of 79 | pass / pass |
| warehouse | pip | 31 of 183 | fail / fail | 1 of 182 | fail / fail |
| httpx | pip | 9 of 74 | fail / pass | 4 of 72 | fail / pass |

- Fresh installs: 10 of 10 fail with refuse, 1 of 10 with filter.
  Warehouse's `requirements/main.in` asks for `PyJWT>=2.15.1`, which
  was uploaded on 2026-09-28.
- Lockfile installs: 3 of 10 fail with refuse, 2 of 10 with filter.
  Fastapi's `uv.lock` and warehouse's `requirements/main.txt` pin
  versions younger than 7 days. Httpx pins only its top-level tools, so
  its 4 young downloads are unpinned transitive dependencies that
  filtering replaces with older versions.
- With filtering, the backstop refused no download in a fresh install.
  In fastapi's lockfile install, it refused the pinned young version.
- Go: `go mod download` of cli/cli's `go.mod` passes in both modes. A
  `go get` of five fast-moving modules `@latest` fails with refuse and
  passes with filter. These runs used the `.info` Time as the clock,
  which S07-egress-gateway now rules out, and `GOSUMDB=off` (see below
  and "Limits"). I76 repeated them with the checksum-database clock and
  `GOSUMDB` on, with the same outcome ("The Go clock end to end").
- crates.io: `cargo fetch --locked` of sharkdp/bat passes in both modes.
  A manifest of nine popular crates with caret ranges resolved 12 young
  crates. It fails with refuse and passes with filter.

### Mapping downloads

- `npm ci` and `uv sync --frozen` don't fetch metadata at all. They take
  the download URL from the lockfile. The gate therefore maps from the
  URL alone and fetches the publish time itself: the full packument for
  npm, and for PyPI the JSON API of the version the file name gives.
  In the final runs, no download had an unknown publish time.
- PyPI: a legacy sdist name with `-` in the project name is ambiguous
  (`foo-bar-1.0.tar.gz`). Checking the exact file name in the JSON API's
  file list for the parsed version finds the right project, and a miss
  is refused.
  The age is per file. A wheel added to an old release later is young on
  its own, and filtering hides it until it ages.
- PyPI clients: pip 26.2 and uv 0.12 both asked for the JSON simple
  index, so the HTML index never needed filtering.
- crates.io: cargo downloads from `/crates/<name>/<version>/download`,
  the form in the index's `config.json`. The crates.io API redirects to
  `<name>-<version>.crate`. The spike's first mapper knew only the
  second form, and failed closed on every crate until both were added.
  All 2,873 index lines of 20 popular crates have a `pubtime`.
- Go: the proxy answers a `.zip` request with a redirect to a signed
  `storage.googleapis.com` URL. The spike's proxy follows it itself, so
  that host doesn't need a policy entry. It followed any target, which
  `wb-proxyd` must not: S07-egress-gateway limits it to one hop to that
  host. The `.info` Time is the commit time, which the module's author
  controls. Of 200 tagged versions that the module proxy first saw in
  the last 7 days, 173 had an `.info` Time more than 7 days earlier
  (median gap 1,087 days). The checksum database adds a record the
  first time a module version is looked up. In 36 samples over 14 days
  of `index.golang.org`, record numbers grew with first-seen time
  (64,217,167 on 2026-09-20, 66,602,558 on 2026-10-04). Neighbors less
  than a second apart sometimes swapped. The record
  number seen at "now minus 7 days" therefore separates young from old,
  with one lookup per module version that never needs repeating,
  because a record number doesn't change. The index itself has 1.12
  million entries a week, which a host would have to keep downloading to
  mirror.
- Homebrew: for 190 of the 200 most installed formulae, the bottle
  manifest on `ghcr.io` names the digest of each platform's bottle and
  its creation time. Its tag is `<version>[_<revision>][-<rebuild>]`.
  The other 10 come from third-party taps, whose downloads can come from
  any host. 61 of the 190 (32%) have a bottle younger than 7 days.
  Homebrew offers one version per formula, and `brew` checks the
  signature of the formula metadata (`formula.jws.json`). `wb-proxyd`
  has no older version to fall back to and no metadata to filter. OSV
  has no Homebrew ecosystem.

### OSV lookups

Each configuration ran twice (n=2), except the cache runs, which ran
once. The proxy logs count requests, and npm fetched some tarballs more
than once: axios's 616 packages took 633 requests and open-webui's
1,077 took 1,119. The 37 vulnerable requests in the open-webui OSV logs are
the 31 packages of the table below.

- One `/v1/query` from the proxy took 230 to 250 ms (median of each
  run), about 300 ms at p95. One at a time from a script, the median
  was 296 ms (p95 353 ms).
- `npm ci` of axios: 18.5 and 22.6 s without OSV, 30.6
  and 32.7 s with a lookup before each download, 30.6 and 37.1 s with
  lookups coalesced into `querybatch`, 15.3 and 14.8 s with the lookup
  overlapped with the download, and 13.0 s with a warm cache.
- `npm ci` of open-webui is bound by download time:
  88.9 and 89.8 s without OSV, 87.9 and 94.2 s with a lookup per
  download. Two `querybatch` runs took 94.8 and 268 s, and a warm-cache
  run that didn't call OSV took 178 s, so network variance on that
  machine, not OSV, made those slow.
- Coalescing doesn't help a proxy: npm keeps at most 15 sockets open
  (`maxsockets`), so each batch is small and also waits for the batch
  window. `querybatch` pays off only for a known list: 1,231 packages
  took 378 s one at a time, 25.6 s with 16 at once, and 2.6 s in 2
  `querybatch` requests.

### Vulnerability threshold

With OSV data on 2026-10-04, these lockfile installs contain a package
with a known vulnerability. Severity is the advisory's
`database_specific.severity` (GitHub advisories).

| Project | Packages | Any vulnerability | HIGH or CRITICAL | CRITICAL |
|---|---|---|---|---|
| actions/toolkit | 966 | 11 | 9 | 1 |
| axios | 616 | 3 | 2 | 0 |
| datasette | 47 | 0 | 0 | 0 |
| undici | 633 | 7 | 7 | 0 |
| open-webui | 1077 | 31 | 20 | 1 |
| flask | 78 | 3 | 3 | 0 |
| fastapi | 190 | 2 | 1 | 0 |
| starlette | 79 | 2 | 1 | 0 |
| warehouse | 182 | 1 | 0 | 0 |
| httpx | 72 | 5 | 2 | 0 |

At HIGH, 8 of the 10 lockfile installs would be refused, at CRITICAL 2.
Most hits are in development tools (`braces`, `brace-expansion`). Some
records have no `database_specific.severity`: the advisories of 6 PyPI
packages have only a CVSS vector, and the RustSec and Go records for
bat and cli/cli have no severity at all. Failing closed on an unknown severity would
refuse those too. None of the hits was a malicious-package report
(`MAL-`).

### The Go clock end to end (I76)

Run on 2026-10-08 with go1.27.1 and `GOSUMDB=sum.golang.org`. The go
command reached the checksum database through the spike's proxy, which
served it on the `GOPROXY` path, so the go command checked every signed
tree it got. The gate took a version's age from its record number and a
calibration point: the lowest record number of the 20 latest versions
that `index.golang.org` shows as first seen at or before 1 minute before
the cutoff, among the first 400 entries from 10 minutes before it. The
spike read one page of 400 entries and didn't page on. At the 3.3
records a second of that hour, 400 entries cover about 2 minutes, so
its 20 versions were about 8 minutes (about 1,600 records) before the
cutoff, not 1 minute. That error is on the safe side, because it only
lowers the point. S07-egress-gateway now asks the implementation to page
the index until it reaches 1 minute before the cutoff.

- **Order.** 320 index entries, 5 at random every 6 hours over 16
  days, all had a record before the spike looked them up. Their record
  numbers follow first-seen time except for 4 pairs, at most 3.8 s
  apart. Against the point, no entry first seen more than 7 days ago
  read as young, and none first seen since read as old. The samples
  were 6 hours apart, and the nearest was about 2 hours from the
  cutoff, so they don't show the boundary itself. The claim that the
  point is on the right side of the cutoff rests on the inversion width
  instead: 3.8 s at most, against the 1-minute margin. The 20 records
  of a calibration window spread over 22 numbers, and the point
  moved 5,974 numbers in 30 minutes. Over the 7 days the database
  added 1.49 records a second.
- **Lookups.** A lookup took 87 ms at the median and 176 ms at p95,
  with 8 at once. One run with 16 at once, next to an install, got a
  connection reset from `sum.golang.org`.
- **Lockfile installs.** `go mod download` of five projects, each with
  its `go.mod` and `go.sum` at a pinned commit, passed in refuse and
  filter mode. None of their 589 module versions was young. The go
  command didn't look anything up itself, because `go.sum` was complete. The
  gate made one per download.

| Project | Modules | No gate (s) | Refuse (s) | Filter (s) |
|---|---|---|---|---|
| `cli/cli` | 179 | 16.2 | 18.8 | 20.0 |
| `junegunn/fzf` | 11 | 2.6 | 3.4 | 3.5 |
| `caddyserver/caddy` | 165 | 31.3 | 32.4 | 32.3 |
| `prometheus/node_exporter` | 54 | 3.9 | 4.6 | 4.5 |
| `gohugoio/hugo` | 180 | 28.1 | 30.5 | 30.6 |

  "No gate" downloads straight from `proxy.golang.org`. The other
  columns also include the spike proxy's own hop and the redirect it
  follows, so the difference is an upper bound on what the clock costs.
- **Fresh install.** `go get` of the five fast-moving modules `@latest`
  resolved one young version, `aws-sdk-go-v2/service/s3` v1.114.1.
  Refuse failed on it, with a message that estimated its publish time
  from its record number. Filter passed in 9.2 s, against 7.0 s with
  the gate only logging. Filtering 21 lists hid 3 versions and took
  3,423 lookups in all, calibration included. The go command's own 29 lookups went through the
  proxy.
- **Filtering `@v/list`.** With one lookup per listed version, 16 at
  once and nothing cached, `github.com/aws/aws-sdk-go` (1,865 versions)
  took 5.6 s, `k8s.io/client-go` (670) 1.8 s, `hashicorp/terraform`
  (476) 1.3 s and `google.golang.org/grpc` (248) 0.75 s. Lists of under
  60 versions took 0.2 s or less, apart from the first, which included
  the calibration. A record number never changes. With a
  cache, a list costs only its new versions. The filter hid 3 versions of
  `aws-sdk-go` that have no record (the checksum database answers 404,
  so the module proxy can't serve them either) and 2 young versions of
  `terraform`. A cheaper filter that checks versions from the highest
  down and stops at the first old one took 1 or 2 lookups per list, but
  missed `terraform` v1.16.5, a young patch release that sorts below an
  old prerelease. A `go get` of that line would then fail on the download
  instead of picking v1.16.4.
- **`index.golang.org` unreachable.** With no calibration point, the
  gate refused all 179 downloads of the cli/cli lockfile install and
  every `@v/list` of the fresh install, each with the rule
  `go-clock-uncalibrated`. With the index taken away after the first
  calibration, both installs passed on the kept point.
- **Versions nobody had looked up.** `go get` of 15 pseudo-versions of
  commits from before 2022, 3 each from five popular modules: 14 were
  in the checksum database already and installed. One cobra commit got
  its record from the gate's own lookup and was refused as young. Under
  this clock it becomes allowed 7 days after that first request. Of the
  first result's 200 versions that the module proxy first saw in the
  7 days before 2026-10-04, 173 have an older commit time. Under this
  clock all 200 were young then, as the order above implies.
- **Branch and commit queries.** `go get foo@master` and
  `go get foo@<sha>` make the go command ask for `@v/master.info` or
  `@v/<sha>.info`. Neither fits the Go path form of S07-egress-gateway,
  a semantic or pseudo-version, and the gate refuses them with a 403.
  The spike's proxy didn't enforce the path forms, and the
  pseudo-version runs above passed the query. I193 asks whether the gate should
  resolve such a query.

## Limits

- The fresh pip and uv installs, with the gate off, built sdists on the
  host. Building an sdist runs the package's own code, outside any
  sandbox. Nothing points to harm, but a spike like this one should use
  `--only-binary=:all:` or `UV_NO_BUILD=1`, or run in a VM.
- The first Go runs used `GOSUMDB=off`, because Go on macOS ignores
  `SSL_CERT_FILE` and `sum.golang.org` couldn't be inspected. go.sum
  hashes were still checked for the lockfile run. The I76 runs served
  the checksum database through the proxy's `GOPROXY` path instead, so
  `GOSUMDB` was on.
- The spike's URL mappers accepted more than they should, such as
  `/@v/master.zip` or a tarball path with a trailing slash. No request
  got through, because the registries answered 404. S07-egress-gateway
  now asks for explicit path forms.
- One run per project per mode for installs, and n=2 for the OSV
  timings, on one machine and network.
- The I76 calibration wasn't single-flight: requests that arrived
  before the first point each calibrated on their own, so the spike made
  more lookups than an implementation would. S07-egress-gateway now
  calibrates on a timer of `wb-proxyd`'s own. The spike also took the
  record number from the lookup's first line over TLS, without checking
  the signed tree that follows it, which S07-egress-gateway now
  requires.
- The spike didn't measure the peak rate at which the checksum
  database adds records: 1.49 a second over 7 days and 3.3 over one
  half hour are the only rates. S07-egress-gateway's check of a fresh
  point uses the verified tree size instead of a rate. The spike didn't
  keep tree heads, so that check is untested. It didn't try more
  than 16 lookups at once, or a window with no index entries.
- Lookups at once: 8 at once ran clean for the 320 samples. At 16 at
  once, the full list-filter run (4,074 lookups) and the fresh `go get`
  with filtering (3,423) ran clean, and one `listcheck.py` run, next to
  an install, got a connection reset. The longest list measured had
  1,865 versions, and the spike didn't look for longer ones.
- The gate's own lookups added a record to the public checksum database
  for the one version nobody had looked up, as the go command would
  have.

## What it means for the specs

- S07-egress-gateway, "Dependency gate": rewritten. Metadata filtering
  in front of the download refusal, the scope of the gate, the
  publish-time source per registry, the Go checksum database as the
  clock, explicit path forms, the Go redirect limited to one hop to its
  storage host, bounded held-back bodies, OSV lookups overlapped with
  the download and cached for an hour, audit events for filtered
  metadata, and Homebrew bottles outside the gate. The package
  registries profile adds `sum.golang.org` read-only and limits
  `ghcr.io` to `homebrew/core`.
- T00-index: T07-ungated-sources and T08-homebrew-ungated.
- S11-verification-and-spikes: fuzz targets for the gate's parsers, the
  conformance case for the gate also checks the metadata and covers Go
  with the real clock and unknown paths, and the `npm ci` benchmark runs
  with the gate on.
- S07-egress-gateway keeps the vulnerability threshold at HIGH until
  I73 is decided.
- I76: S07-egress-gateway, "Publish time", now says how the Go
  calibration point is found, bounded by the verified tree size
  (`go-clock-implausible`), refreshed on `wb-proxyd`'s own timer, kept
  when a refresh fails (`go-clock-stale`), and persisted in `state.db`
  through `wb-hostd`. The gate refuses every Go download and listing
  without a point (`go-clock-uncalibrated`), and verifies each lookup
  against the signed tree head with the compiled-in key
  (`go-clock-lookup-failed`, and `go-sumdb-inconsistent` for a tree
  that doesn't extend the last one). It caches verified record numbers
  per VM, and caps lookups per VM and per `wb-proxyd`
  (`go-clock-lookup-rate`) and versions per list (`go-list-too-long`).
  "Downloads" says that only a Go `.zip` is age-gated. S04-architecture
  adds the clock lookups to "Host work the guest can cause".
  S11-verification-and-spikes adds the unreachable-index case and a
  case for each new rule, against doubles of both services, to the
  conformance case for the gate, and a check to the path mapper's fuzz
  target.

## Spike code

Branch `spike/x21-dep-gate-registries`, commit
[`7955742`](https://github.com/wraithbox/wraithbox/tree/79557420e88339946a7debf84df57d4ccc40ba36/spikes/x21-dep-gate-registries):
the proxy, the harness, the project list, and the raw tables. The I76
runs are in `proxy/sumdbclock.go`, `clock.py`, `goclock.py`,
`listcheck.py` and `results-i76.md`. The first result was measured at
commit `56f264a`, tagged `spike-x21-dep-gate-registries`.

**Status:** Answered 2026-10-04: yes, with conditions
