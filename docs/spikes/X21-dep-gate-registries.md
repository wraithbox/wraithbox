# X21 - Dependency gate on real registries

Brief: B32-dep-gate-registries

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
  version becomes allowed. OSV lookups run while the download runs and
  are cached for an hour.
- **Controls touched:** SEC07-dep-gate is kept: every download of a
  too-young version is still refused, and the metadata filter comes in
  front of that refusal. SEC07-dep-gate lists npm, PyPI, Go modules and
  crates, not Homebrew. S07-egress-gateway now says that Homebrew
  bottles are not gated, and why.
- **Assumed:** that the registries keep publishing the fields measured
  here: npm `time`, PyPI `upload-time`, crates.io `pubtime`, and the
  Go checksum database's record numbers.
- **Open decisions:** the vulnerability threshold. At the specified
  default (HIGH), OSV data would refuse the lockfile installs of 8 of
  the 10 projects. This result doesn't change that default. I73 asks
  the maintainer to decide.
- **Brief:** B32-dep-gate-registries

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
| Go module proxy | `<module>/@v/<version>.zip` | not the `.info` Time, which is the commit time; the record number in `sum.golang.org` instead | `@v/list` and `@latest` | yes, with the checksum database as the clock |
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
`pip install -r` of the pinned file. "Young" is the number of
downloads, out of all downloads, that were younger than 7 days with the
gate off.

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
  passes with filter. These runs used the `.info` Time as the clock (see
  below).
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
  that host doesn't need a policy entry. The `.info` Time is the commit time, which the
  module's author controls. Of 200 tagged versions that the module proxy
  first saw in the last 7 days, 173 had an `.info` Time more than 7 days
  earlier (median gap 1,087 days). The checksum database adds a record
  the first time a module version is looked up. Across 14 days of
  `index.golang.org` samples, record numbers grew with first-seen time
  (64,217,167 on 2026-09-20, 66,602,558 on 2026-10-04). The record
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
  has no older version to fall back to and no metadata to filter. OSV has no
  Homebrew ecosystem.

### OSV lookups

- One `/v1/query` takes 230 to 250 ms (median), about 300 ms at p95.
- `npm ci` of axios (633 downloads): 18.5 and 22.6 s without OSV, 30.6
  and 32.7 s with a lookup before each download, 30.6 and 37.1 s with
  lookups coalesced into `querybatch`, 15.3 and 14.8 s with the lookup
  overlapped with the download, and 13.0 s with a warm cache.
- `npm ci` of open-webui (1,119 downloads) is bound by download time:
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
records have no `database_specific.severity`: the advisories of 6 PyPI packages have
only a CVSS vector, and the RustSec and Go records for bat and cli/cli
have no severity at all. Failing closed on an unknown severity would
refuse those too. None of the hits was a malicious-package report
(`MAL-`).

## What it means for the specs

- S07-egress-gateway, "Dependency gate": rewritten. Metadata filtering
  in front of the download refusal, the publish-time source per
  registry, the Go checksum database as the clock, mapping from the URL
  alone, OSV lookups overlapped with the download and cached for an
  hour, and Homebrew bottles outside the gate.
- S11-verification-and-spikes: the conformance case for the gate also
  checks that a too-young version is missing from the metadata, and the
  `npm ci` benchmark runs with the gate on.
- S07-egress-gateway keeps the vulnerability threshold at HIGH until
  I73 is decided.

## Spike code

Branch `spike/x21-dep-gate-registries`, commit
[`671d7a9`](https://github.com/wraithbox/wraithbox/tree/671d7a909b0f7c8c23f5fe5c8ab16cc779c5c7d2/spikes/x21-dep-gate-registries):
the proxy, the harness, the project list, and the raw tables.

**Status:** Answered 2026-10-04: yes, with conditions
