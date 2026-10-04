# S07 - Egress Gateway

**Purpose:** The only path from a guest to any network: a userspace
network stack (`wb-netd`) and a policy-enforcing proxy (`wb-proxyd`) on
the host.

**Requirements:** FR08-no-proxy-config to FR10-learn-mode, SEC04-no-guest-secrets to SEC07-dep-gate, SEC10-audit, SEC11-root-gains-nothing, SEC13-bounded-resources, NFR06-explained-refusals.

## Packet path (`wb-netd`)

- **Packet transport.** Each VM's virtual NIC is connected to its
  `wb-netd` by a platform-specific packet transport (S12-platforms) that
  needs no host privileges and no host network device. On macOS,
  `wb-vmd` creates a datagram socketpair per VM, gives one end to the
  VM's network device (file-handle attachment) and passes the other to
  that VM's `wb-netd` through `wb-hostd` (`SCM_RIGHTS`). Every Ethernet
  frame the guest sends arrives in `wb-netd`; the guest has no other
  network path. The rest of this spec is the same on every platform.
- **Stack.** gVisor's userspace TCP/IP stack (`pkg/tcpip`), consumed as a
  Go module from gVisor's `go` branch. Its file-descriptor link endpoint
  is Linux-only, so Wraith Box provides a small link endpoint that moves
  frames between the packet transport and the stack, with one transport
  adapter per platform.
- **Addressing.** `wb-netd` runs a DHCP server handing the guest a single
  private IPv4 address, with the gateway as router and DNS server. Only
  the guest's own MAC and leased address are accepted. IPv6 is not
  offered and all IPv6 frames are dropped.
- **DNS** (UDP and TCP port 53 on the gateway only):
  - `A` queries for allowlisted names are answered with a **synthetic
    address** from a reserved range (198.18.0.0/15), allocated per name
    and remembered with a TTL. Real upstream addresses are never revealed
    to the guest.
  - `AAAA` queries get an empty answer; other record types are refused.
  - Names not on the allowlist get `NXDOMAIN` and raise an approval
    event (FR09-approve-unknown). After approval, the next lookup succeeds.
  - Wildcard allowlist entries are bounded: each session may resolve at
    most a fixed number of new names under wildcards, and failed lookups
    count against that budget, because names themselves can carry data
    (T02-dns-names). Query rate and name length are limited.
- **Connections.**
  - TCP to a synthetic address on an allowed port is accepted by the
    stack and handed to `wb-proxyd` as a byte stream over a Unix socket,
    tagged with VM, project, session, hostname, and port.
  - TCP to any other address is reset; UDP other than DNS is dropped
    (clients fall back from QUIC to TCP); ICMP is answered only for the
    gateway address. The host, the LAN, and raw IP destinations are
    therefore unreachable by construction (SEC05-default-deny).

## Stream path (`wb-proxyd`)

- **Name binding.** The hostname comes from the synthetic address. For
  TLS, the ClientHello SNI must equal that hostname or the stream is
  reset. `wb-proxyd` resolves the real upstream address itself.
- **Modes, per host:**
  - **inspect** (default): terminate TLS with a leaf certificate from the
    Wraith Box CA (S09-policy-credentials-audit), apply HTTP policy and credential
    replacement, then open a new TLS connection upstream validated
    against the host's system trust store. HTTP/1.1 and HTTP/2.
  - **pass**: relay TLS bytes unchanged to the named host. Only for hosts
    where inspection breaks the client; every pass-through host is a
    documented residual channel, because the proxy cannot see what is
    sent (T01-allowed-channels).
  - Plain HTTP is allowed only when policy names `host:80`, and is
    always inspected.
- **Credential replacement** (SEC04-no-guest-secrets). On inspected
  hosts, `wb-proxyd` removes the credentials the guest sends, whatever
  the method: `Authorization`, `Proxy-Authorization` and `Cookie` on
  every host, plus each header the host's built-in profile names (the
  model API's `x-api-key`, GitLab's `PRIVATE-TOKEN` and `JOB-TOKEN`). A
  request with a query parameter that the profile names as a credential
  (GitLab's `private_token` and `access_token`) is refused with a `403`
  that names the rule, because GitLab reads a token from the query
  string (X22-no-guest-credentials). If the host has a credential
  binding, the binding's credential is then injected
  (S09-policy-credentials-audit). A token supplied by an attacker is
  therefore never forwarded, and the guest only ever holds
  placeholders. Each removal and refusal is logged with the header or
  parameter name and the rule, never the value (SEC10-audit).
  - *Anonymous credentials.* Some hosts answer public reads only to a
    request with a token. `wb-proxyd` doesn't forward the
    guest's token for those, not even one the host issued in the same
    session. A host-side binding with a fixed public value injects one
    instead. The package registries profile has one:
    `Authorization: Bearer QQ==` on GET and HEAD of `ghcr.io`
    `/v2/homebrew/core/`, the value Homebrew itself sends, and writes
    to those paths are refused. Without it, every bottle download fails
    with a 401, because `brew` never asks the token endpoint for a
    token. Forwarding guest tokens on reads only was rejected: it
    forwards an attacker's token on every GET, and a placeholder
    without a binding then fails the request. Forwarding only tokens
    that the host issued in this session was rejected because it
    doesn't fix Homebrew.
  - *What is left.* Server-issued credentials in a download address
    (presigned storage URLs, as for git LFS and Homebrew bottles) pass
    unchanged. Removing them would break those downloads, and a
    storage host still has to be allowed by policy. A
    guest's own credential in a header or parameter that no profile
    names reaches a host without a built-in profile
    (T09-unnamed-credentials).
- **Placeholder binding.** Each placeholder is bound to the hosts,
  ports, and paths of its binding, following OpenShell's provider
  model. A placeholder found anywhere else in a request (another host,
  a query string, a body) gets a `403` naming the binding, and an audit
  event. The placeholder itself is never sent upstream. For AWS,
  `wb-proxyd` signs requests (SigV4) rather than injecting a key.
- **HTTP policy** (SEC06-repo-writes). Rules match method and path per host, GraphQL
  operation type and name, and WebSocket messages, in the OpenShell
  policy schema (S09-policy-credentials-audit). Each inspected host enforces its rules by
  default. A host can be set to `audit` while a new rule is tried out:
  violations are then logged but allowed. Built-in profiles:
  - *git hosting*: reads allowed; `git-receive-pack` and mutating API
    calls only for the project's repositories (derived from the host
    repository's remotes, plus explicit additions); gists, repository
    creation and forks denied. GraphQL mutations are denied unless the
    operation name is allowlisted.
  - *model API*: the endpoints Claude Code needs, with the model
    credential binding.
  - *package registries*: metadata and downloads only, publish and
    upload endpoints denied. npm, PyPI, crates.io and the Go module
    proxy, plus `sum.golang.org` read-only (`/lookup/`, `/latest`,
    `/tile/`), because the module proxy doesn't serve the checksum
    database. Homebrew bottles: `ghcr.io` only for `homebrew/core` (the
    anonymous token's `repository:homebrew/core/...:pull` scope and
    `/v2/homebrew/core/...` paths), and `formulae.brew.sh` read-only.
- **Dependency gate** (SEC07-dep-gate). For npm, PyPI, the Go module
  proxy and crates.io, `wb-proxyd` looks up each version's publish time
  and known vulnerabilities (OSV data). The minimum age (default 7 days)
  is applied twice, because refusing only the download fails nearly
  every fresh install (X21-dep-gate-registries):
  - *Metadata.* Versions younger than the minimum age are removed from
    the registry's metadata responses, and a resolver then picks an
    older version. This covers npm packument `versions` and
    `dist-tags` (with `latest` moved to the newest remaining release),
    the PyPI JSON simple index (PEP 691), crates.io sparse index lines,
    and Go `@v/list` and `@latest`. npm `/-/package/<name>/dist-tags`
    is filtered like the packument's `dist-tags`. The PyPI JSON API
    (`/pypi/<name>/json`, which Poetry reads) loses its young
    `releases`, and when its `info` describes a young release the
    response is refused. A PyPI client that doesn't accept the JSON
    simple index is refused. The maintainer accepted these three rules
    on I32.
  - *Downloads.* A download of a version younger than the minimum age,
    or with a known vulnerability at or above the threshold, is
    refused. The error names the package, the version, its publish time
    and the date it becomes allowed. On the gated hosts this catches
    lockfile pins, which never fetch metadata.

  *Vulnerability threshold.* The default threshold is CRITICAL (I73).
  A malicious-package report (an OSV ID starting `MAL-`) is refused
  whatever its severity. A vulnerability below the threshold is
  allowed, logged with the advisory and the rule `vuln-below-threshold`
  (SEC10-audit), and shown in `wb status`. The severity is the record's
  `database_specific.severity`. Where that is missing, `wb-proxyd`
  scores the record's CVSS vector. A record with no severity at all is
  allowed and logged with the rule `vuln-no-severity`. A failed lookup
  is still refused. At HIGH, 8 of 10 lockfile installs measured in
  X21-dep-gate-registries would have been refused, mostly for
  development tools, so users would have learned to override.

  *Scope.* The gate covers the registry hosts above and nothing else.
  The same packages fetched another way are not gated: Go with
  `GOPROXY=direct` or `GOPRIVATE` from a git host, `git+https:` and
  `github:` dependencies and archive downloads from a git host
  (`codeload.github.com`), cargo `git` dependencies, and mirrors such
  as `registry.npmmirror.com`, `goproxy.cn` or an Artifactory server.
  That residual is T07-ungated-sources. Two rules narrow it: the git
  hosting profile denies archive downloads, and the approval risk check
  flags a host that mirrors a gated registry. The maintainer accepted
  T07-ungated-sources with both rules on I32.

  *Publish time.* From the registry, fetched by `wb-proxyd`: npm `time`
  from the full packument (the abbreviated one has none), PyPI
  `upload-time` per file (PEP 700, so a file added to an old release
  later is young on its own), and crates.io `pubtime` from the sparse
  index. For Go, the clock is the version's record number in the
  checksum database `sum.golang.org`, compared with the record number of
  a version that `index.golang.org` shows as first seen 7 days earlier.
  The `.info` Time is the commit time, which the module's author sets,
  and is never used. Under that clock, a version nobody has looked up
  before is young, a pseudo-version of an old commit is young for 7
  days, and filtering `@v/list` costs one lookup per listed version.
  The spike checked only the ordering of record numbers, and I76
  measures the clock end to end.

  *Mapping a request.* Each gated host accepts only explicit path forms:
  an npm package name and tarball, a PEP 503 project name with a PEP 440
  version in a distribution file name, a Go escaped module path with a
  semantic or pseudo-version, and a crate name with a semantic version.
  A request that fits none of them gets a 403 that names the rule.
  Parsed names and versions are validated before they go into a lookup
  URL or an OSV query. A download is mapped from its URL alone, and a
  PyPI file name is checked against the project's JSON API. The Go
  module proxy answers a `.zip` request with a redirect. `wb-proxyd`
  follows it itself, one hop, and only to
  `https://storage.googleapis.com/`. Any other `Location` is refused
  and logged with the rule, so the redirect can't reach the host, the
  LAN or another site (SEC05-default-deny).

  *Holding the body.* OSV is queried while the download runs, and no
  byte of the body reaches the guest before the verdict. A held body is
  buffered in memory up to a fixed size per stream and spooled to a
  bounded temporary file beyond it. The number of gated downloads in
  flight per VM is bounded (SEC13-bounded-resources). OSV answers are
  cached for one hour, keyed on package, version and threshold. During
  an OSV outage only answers less than an hour old are used, and every
  other download is refused. A malicious-package report published within
  that hour can be missed, a residual the maintainer accepted on I32.
  If the publish time can't be found or a lookup fails, the
  request is refused (fail closed). Per-project overrides are explicit
  and audited.

  *Logging.* Each filtered metadata response is an audit event with the
  package, the versions hidden and the rule `min-age` (SEC10-audit).
  `wb status` shows it, so a package with only young versions reads as
  too new and not as a typo (NFR06-explained-refusals). On gated
  metadata requests `wb-proxyd` removes `If-None-Match` and
  `If-Modified-Since`, and a rewritten body goes out without `ETag` or
  `Last-Modified`.

  *Private registries.* The gate fetches publish times itself, before
  credentials are injected, so a package on a private registry fails
  closed. How the gate authenticates its own fetch is open
  (B32-dep-gate-registries).

  *Homebrew.* Bottles are not gated (T08-homebrew-ungated). Homebrew
  offers one version per formula and signs its formula metadata, so
  there is nothing to filter and no older version to fall back to. OSV
  has no Homebrew data. With a 7-day minimum age, about a third of the
  most installed formulae would be refused. Casks (`homebrew/cask`) are
  outside the `ghcr.io` scope in V1, so `brew install --cask` raises an
  approval prompt for the host its download comes from.

  The gate implements OpenShell's supervisor middleware API
  (`SupervisorMiddleware`, RFC 0009) and runs after policy allows a
  request and before credentials are injected, so it never sees a
  credential. It runs inside `wb-proxyd`.

## Approvals and learning

- An unknown destination produces an approval event in `wb-hostd`, shown
  as a native notification and listed by `wb status`: *allow for this
  session*, *allow for this project*, or *deny*. Each request shows the
  result of the prover's risk check on the rule it would add (S09-policy-credentials-audit),
  such as new reach for a credential or a new write method. Policy
  changes take effect without restarting anything.
- **Learn mode** (trusted projects only, FR10-learn-mode): DNS resolves any name and
  connections are inspected and allowed, while credentials stay host-side
  as usual. The session's destinations become a suggested allowlist for
  `wb learn report`.

## Performance

Packets are processed in userspace, so throughput is lower than kernel
networking. S11-verification-and-spikes sets a benchmark for large downloads. If throughput
misses it, the fix is inside `wb-netd` (batching, buffer sizes), not a
second network path.

**Status:** Draft
