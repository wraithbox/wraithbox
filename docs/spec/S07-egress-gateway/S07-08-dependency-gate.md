# S07-08 - Dependency Gate

**Purpose:** How `wb-proxyd` keeps young and vulnerable package
versions from the guest on the gated registries.

## Design

### Dependency gate

This page serves SEC07-dep-gate. For npm, PyPI, the Go module proxy
and crates.io, `wb-proxyd` looks up each version's publish time and
known vulnerabilities (OSV data). Publish times are on their own page,
S07-09-publish-time.

The minimum age is 7 days by default. It is applied twice, to metadata
and to downloads.

#### Metadata

Versions younger than the minimum age are removed from the registry's
metadata responses. A resolver then picks an older version. This
covers:

- npm packument `versions` and `dist-tags`, with `latest` moved to the
  newest remaining release;
- npm `/-/package/<name>/dist-tags`, filtered like the packument's
  `dist-tags`;
- the PyPI JSON simple index (PEP 691);
- crates.io sparse index lines;
- Go `@v/list` and `@latest`.

For PyPI:

- The PyPI JSON API (`/pypi/<name>/json`, which Poetry reads) loses
  its young `releases`.
- When that response's `info` describes a young release, the response
  is refused.
- A PyPI client that doesn't accept the JSON simple index is refused.

#### Downloads

A download is refused when the version is younger than the minimum
age, or has a known vulnerability at or above the threshold. The error
names the package, the version, its publish time, and the date it
becomes allowed.

On the gated hosts this catches lockfile pins, which never fetch
metadata.

For the Go module proxy, only `.zip` is a download.

- `.info` and `.mod` are not age-gated. The path forms ("Mapping a
  request") still apply to them.
- The clock lookup key and the upstream download URL are built from
  the same parsed module path and version. The fuzz target of the path
  mapper (C07-dependency-gate) checks that they agree.

### Vulnerability threshold

The default threshold is CRITICAL (I73).

- A malicious-package report, an OSV ID starting `MAL-`, is refused
  whatever its severity.
- A vulnerability below the threshold is allowed. It is logged with
  the advisory and the rule `vuln-below-threshold` (SEC10-audit), and
  shown in `wb status`.

The severity is the record's `database_specific.severity`. Where that
is missing, `wb-proxyd` scores the record's CVSS vector.

- A record with no severity at all is allowed, and logged with the
  rule `vuln-no-severity`.
- A failed lookup is still refused.

### Scope

The gate covers the registry hosts above, and nothing else. The same
packages fetched another way are not gated:

- Go with `GOPROXY=direct`, or `GOPRIVATE` from a git host;
- `git+https:` and `github:` dependencies;
- archive downloads from a git host (`codeload.github.com`);
- cargo `git` dependencies;
- mirrors such as `registry.npmmirror.com`, `goproxy.cn` or an
  Artifactory server.

That residual is T07-ungated-sources. These rules narrow it:

- the git hosting profile denies archive downloads;
- the approval risk check flags a host that mirrors a gated registry.

### Mapping a request

Each gated host accepts only explicit path forms:

| Registry | Path form |
|---|---|
| npm | a package name and tarball |
| PyPI | a PEP 503 project name, with a PEP 440 version in a distribution file name |
| Go module proxy | an escaped module path, with a semantic or pseudo-version |
| crates.io | a crate name, with a semantic version |

A request that fits none of them gets a 403 that names the rule.

The gate identifies a package by its percent-decoded name. So
`/@scope/name`, `/@scope%2fname` and `/@scope%2Fname` get the same
decision. A request on a gated host whose package can't be identified
is refused.

- Parsed names and versions are validated before they go into a
  lookup URL or an OSV query.
- A download is mapped from its URL alone.
- A PyPI file name is checked against the project's JSON API.

#### Go module redirects

The Go module proxy answers a `.zip` request with a redirect.
`wb-proxyd` follows it itself, one hop, and only to
`https://storage.googleapis.com/`. Any other `Location` is refused and
logged with the rule. So the redirect can't reach the host, the LAN or
another site (SEC05-default-deny).

### Holding the body

OSV is queried while the download runs. No byte of the body reaches
the guest before the verdict.

- A held body is buffered in memory up to a fixed size per stream, and
  spooled to a bounded temporary file beyond it.
- The number of gated downloads in flight per VM is bounded
  (SEC13-bounded-resources).

OSV answers are cached for one hour, keyed on package, version and
threshold. During an OSV outage, only answers less than an hour old
are used. Every other download is refused. A malicious-package report
published within that hour can be missed.

If the publish time can't be found or a lookup fails, the request is
refused (fail closed).

Per-project overrides are explicit and audited. They apply to the
whole VM while the project has a session in it (S07-01-per-vm-policy).

### Logging

Each filtered metadata response is an audit event, with the package,
the versions hidden and the rule `min-age` (SEC10-audit). `wb status`
shows it. So a package with only young versions reads as too new, and
not as a typo (NFR06-explained-refusals).

On gated metadata requests, `wb-proxyd` removes `If-None-Match` and
`If-Modified-Since`. A rewritten body goes out without `ETag` or
`Last-Modified`.

### Private registries

The gate fetches publish times itself, before credentials are
injected. So a package on a private registry fails closed. How the
gate authenticates its own fetch is open (B32-dep-gate-registries).

### Homebrew

Bottles are not gated (T08-homebrew-ungated).

Casks (`homebrew/cask`) are outside the `ghcr.io` scope in V1. So
`brew install --cask` raises an approval prompt for the host its
download comes from.

### OpenShell middleware

The gate implements OpenShell's supervisor middleware API
(`SupervisorMiddleware`, RFC 0009). It runs inside `wb-proxyd`, after
policy allows a request and before credentials are injected. So it
never sees a credential.

#### In process

The gate implements the Go interface generated from
`supervisor_middleware.proto` (package `openshell.middleware.v1`,
OpenShell v0.1.2, S10-tech-stack). `wb-proxyd` calls it as a Go
method, with no listener and no gRPC hop (X24-openshell-artifacts).

- Download checks use `EvaluateHttpRequest`.
- The metadata filter uses the messages of the `HttpResponsePreReturn`
  service, which the spike didn't exercise.
- An error returned by a gate method is a refusal.

#### Not bound by OpenShell's proxy

OpenShell's own proxy doesn't offer a middleware the body of a
compressed response. It doesn't pass a middleware's free-form deny
reason to the client either. Those limits belong to OpenShell's
caller, not the API.

`wb-proxyd` is the caller here. So it writes its own refusal message.
It decodes metadata before filtering, under these rules
(SEC13-bounded-resources):

- It sets `Accept-Encoding` on the upstream request itself, to the
  encodings it decodes.
- It caps the decoded size, and the ratio of decoded to received
  bytes. A response past either cap is refused with a 403 that names
  the rule (NFR06-explained-refusals).
- It assembles the whole body before filtering. It spools a body past
  its memory limit to disk, within the size cap.

## Decisions

### Apply the minimum age twice

The gate filters young versions out of metadata, and refuses young
downloads, with the three PyPI rules above.

- **Why:** refusing only the download fails nearly every fresh install
  (X21-dep-gate-registries).
- **Decided:** I32 accepted the three PyPI rules.

### Threshold CRITICAL

- **Why:** at HIGH, 8 of 10 lockfile installs measured in
  X21-dep-gate-registries would have been refused, mostly for
  development tools. Users would have learned to override.
- **Decided:** I73.

### Accepted residuals

T07-ungated-sources is accepted, with the two rules of "Scope". So is
a malicious-package report missed within the hour an OSV answer is
cached ("Holding the body").

- **Decided:** I32.

### Bottles not gated

- **Why:** Homebrew offers one version per formula, and signs its
  formula metadata. So there is nothing to filter, and no older
  version to fall back to. OSV has no Homebrew data. With a 7-day
  minimum age, about a third of the most installed formulae would be
  refused.

**Status:** Draft
