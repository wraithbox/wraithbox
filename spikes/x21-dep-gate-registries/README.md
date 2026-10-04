# X21-dep-gate-registries spike (throwaway)

Throwaway code for issue #32. Not held to the project gates, never merged.
The written result is `docs/spikes/X21-dep-gate-registries.md` on `main`.

Run on 2026-10-04, macOS 27 on Apple Silicon, against the public registries.

## What is here

- `proxy/main.go`: an inspecting HTTPS proxy on 127.0.0.1 with a throwaway
  CA (generated per run, name-constrained to the registry hosts, trusted
  only per process through `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`,
  `PIP_CERT`, `CARGO_HTTP_CAINFO`, and deleted with the temp dir). It maps
  every download to package, version and publish time for npm, PyPI, the
  Go module proxy and crates.io, and applies a 7-day minimum age with one
  of two strategies:
  - `refuse`: metadata unchanged, a too-young download gets a 403.
  - `filter`: too-young versions removed from metadata (npm packument and
    dist-tags, PyPI JSON simple index, crates.io sparse index lines, Go
    `@v/list` and `@latest`), and the download refusal kept as a backstop.
  - `off`: logs what would be refused.
  Optional OSV lookup per download: `-osv single|batch`, `-osv-cache`,
  `-osv-overlap` (lookup runs while the download runs).
- `harness.py`, `projects.json`: 10 real projects (5 npm, 3 uv, 2 pip) at
  pinned commits, lockfile install and fresh install (lockfile removed or
  pins stripped), in each mode, in temp dirs outside the worktree.
- `othereco.py`: the same for Go (cli/cli `go.mod`/`go.sum`; `go get` of
  fast-moving modules `@latest`) and cargo (sharkdp/bat `Cargo.lock`; a
  manifest of popular crates with caret ranges).
- `mapping.py`: publish-time sources for Go, crates.io and Homebrew.
- `gosumdb.py`: checksum-database record numbers against first-seen times.
- `osvbench.py`, `osvruns.py`, `osvoverlap.py`, `osvstats.py`: OSV timing.
- `vulnsev.py`: how many lockfile installs a severity threshold would refuse.
- `analyze.py`: the tables in `results-*.md`.

## Measured output (copied from the runs)

```text
mapping.py crates
crates: 20 crates, 2873 index lines, without pubtime: 0, younger than 7 days: 4
crates: api download redirects (302) to https://static.crates.io/crates/serde/serde-1.0.200.crate

mapping.py homebrew
homebrew: top 200 formulae by 30d installs, bottle created time known for 190, younger than 7 days: 61 (32%)
homebrew: notes {'digest-in-index': 190, 'error HTTP Error 404: Not Found': 10}
  (the 10 are third-party taps: hashicorp/tap/terraform, supabase/tap/supabase, ...)

mapping.py go
go: index entries in the last 7 days: 1121505 (561 pages of 2000)
go: tagged versions first seen in the last 7 days, sampled: 200; .info Time more than 7 days before first-seen: 173 (86%)
go: gap first-seen minus .info Time, days: median 1087.4, p90 3407.3, max 5008

gosumdb.py: record numbers grow with index.golang.org first-seen time across
14 days (64217167 on 2026-09-20 to 66602558 on 2026-10-04); within one
second, neighbors can swap by one or two.

osvbench.py open-webui
open-webui: 1231 packages
single-seq: 378.0s total, per query median 296 ms, p95 353 ms; packages with vulns: 31
single-16: 25.6s total
batch: 2.6s total for 1231 queries (2 requests); vuln ids returned: 96

vulnsev.py (lockfile installs; severity from database_specific.severity)
actions-toolkit  966 pkgs  any vuln 11  HIGH+ 9  CRITICAL 1
axios            616 pkgs  any vuln  3  HIGH+ 2  CRITICAL 0
cargo (bat)      316 pkgs  any vuln  4  HIGH+ 0  severity unknown 4 (no CVSS vector either)
datasette         47 pkgs  any vuln  0
fastapi          190 pkgs  any vuln  2  HIGH+ 1  unknown 1 (has CVSS vector)
flask             78 pkgs  any vuln  3  HIGH+ 3
go (cli/cli)     179 pkgs  any vuln  1  HIGH+ 0  severity unknown 1 (no CVSS vector)
httpx             72 pkgs  any vuln  5  HIGH+ 2  unknown 3 (have CVSS vectors)
open-webui      1077 pkgs  any vuln 31  HIGH+ 20 CRITICAL 1
starlette         79 pkgs  any vuln  2  HIGH+ 1  unknown 1 (has CVSS vector)
undici           633 pkgs  any vuln  7  HIGH+ 7
warehouse        182 pkgs  any vuln  1  HIGH+ 0  unknown 1 (has CVSS vector)
No MAL- (malicious package) entries in any of them.
```

The install matrix is in `results-matrix.md` and `results-go-cargo.md`,
the OSV timings per download in `results-osv.txt`.

## Hygiene

The fresh pip and uv installs built sdists on the host, which runs package
build code outside any sandbox, and the Go runs used `GOSUMDB=off`. A
future spike of this kind should use `--only-binary=:all:` or
`UV_NO_BUILD=1`, or run inside a VM.
