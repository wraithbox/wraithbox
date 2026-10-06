# S11 - Verification and Open Questions

**Purpose:** How the requirements are proven, and which assumptions must
be tested before building on them.

## Test layers

- **Unit tests.** Go `go test -race`, table-driven; Swift Testing.
- **Fuzzing.** Go native fuzz targets for every parser that sees
  guest-controlled bytes: Ethernet/IP/TCP handling at the link endpoint,
  DHCP, DNS, TLS ClientHello parsing, HTTP/1.1 and HTTP/2 request
  handling, request path canonicalization, the credential parameter
  scan of query strings and of form and JSON bodies
  (X22-no-guest-credentials), policy matching, the git transport framing (the request
  header and the push command list, X07-git-round-trip), the pack
  scanner in front of `receive-pack` and the input of the pre-receive
  check (X26-pre-receive-check), every vsock RPC
  handler in `wb-hostd`, the stream framing that multiplexes guest
  requests on a host-opened connection, with the project each request
  is attributed to (S04-architecture, B29-vsock-handoff), network policy YAML, the guest flow labels
  of S13-guest-confinement, and the dependency gate's parsers: npm
  request paths, PyPI distribution file names, Go escaped module paths
  and versions, crates.io index lines, and `sum.golang.org` lookup
  responses (Swift: property-based tests where fuzzing is impractical).
  The terminal filter in `wb` (S05-cli, "Terminal stream") is one of
  them. Its fuzz target checks these properties, with the BEL rate
  limit off:
  - every output token is one the allowlist passes unchanged, a BEL
    that replaced a notification, or U+FFFD for an invalid byte;
  - filtering twice gives the bytes of filtering once;
  - where a read splits the input doesn't change the output;
  - no OSC 0 to 2, 8, 52 or 1337 survives, and no DCS, APC, PM, or SOS
    introducer;
  - no parameter exceeds the caps, and no `J` or `K` has a parameter
    above 2;
  - the link list holds at most 100 entries, and marks every URL that
    matches a mark rule of S05-cli;
  - the filtered output followed by the exit reset, replayed into a
    headless terminal emulator, leaves it in its initial state: modes,
    kitty keyboard stacks on both screens, keypad mode, character sets,
    scroll region, tab stops, and colors and style. For each of these
    states, a test removes its step from the reset and sees the
    property fail, for example `ESC =` with no `ESC >`.

  The pack scanner's target checks that what it forwards equals its
  input when a pack passes, and is a prefix of it when one is refused.
  Its seeds include REF_DELTA entries, delta chains, empty zlib stored
  blocks, and packs written by zlib-ng at level 1 and with
  `core.compression=0`. A differential test feeds the scanner's corpus
  and the generated packs to `git index-pack --stdin --strict`: the two
  must agree on entry boundaries and size fields, and every pack the
  scanner refuses as malformed git refuses too.

  The link endpoint's targets include the link filter of
  S07-egress-gateway and its ARP branch, with a corpus of frames from
  0 to 65535 + 14 bytes, and seeds of truncated reads that fill the
  MTU + 15 byte buffer, which must be dropped.
- **Conformance suite.** A set of adversarial checks run *inside a guest*
  against a real `wb-netd`/`wb-proxyd`. Each must fail safely, and the
  suite is a release gate for every host/guest combination in the
  platform matrix (S12-platforms); a combination is not supported until it
  passes. OpenShell's adversarial end-to-end tests (`e2e/rust/tests/`:
  bypass detection, L7 proxy bypass, credential gating) are the first
  source of further cases:
  - connect to a raw IP address, to the host, to the LAN, over IPv6;
    ping them and a synthetic address and see that nothing answers;
    check that nothing answers the guest's ARP probes for its own
    address, and that the lease holds only the options in
    S07-egress-gateway's list (X03-network-path); send a forged ARP
    reply, a fragment, an IPv4 packet with options and a frame to
    another destination MAC, and see each dropped with its rule;
  - with the gateway's empty answers to `HTTPS` and `SVCB`, check that
    the macOS resolver still connects to an allowlisted name as fast as
    it did when they were refused (B17-network-path);
  - resolve a name on the guest OS's background list and see
    `NXDOMAIN`, an audit entry and no approval event, also when it is
    sent in mixed case and with a trailing dot; resolve a name one
    label longer under it and see an approval event; see the same
    answer bytes for a name that is not allowlisted, denied, on the
    list or in a part that is off (S07-egress-gateway, quiet
    refusals);
  - send UDP other than DNS; resolve a non-allowlisted name; exceed the
    wildcard budget; use a DNS server other than the gateway;
  - present an SNI that differs from the resolved name;
  - push to a repository outside the project set, with and without an
    attacker-supplied token; create a gist or repository; publish a
    package. The project set is the VM's: the repositories of every
    project with a session in the VM (S07-egress-gateway, "Enforced per
    VM");
  - per-VM enforcement (S07-egress-gateway, "Enforced per VM"):
    - end a project's last session and see its binding no longer
      injected, its open streams closed with the rule
      `policy-recomputed`, and its project user's processes stopped;
    - give two projects different wildcard budgets or minimum ages,
      start both, and see the stricter one applied to both, including
      to a count reached before the second one joined;
    - with the prover unavailable, remove a binding while sessions
      run and see it no longer injected and its streams closed, then
      add a rule, and separately remove a `deny_rules` entry, and see
      each change refused and shown as pending;
    - start a second project whose policy puts a host in pass mode
      that the first project binds or inspects, whose binding overlaps the first
      one's on the same host, port and path with another secret, or
      whose rules leave the boundary in union, and see the start
      refused with both projects named and the first project's policy
      unchanged;
    - with the prover unavailable, end the last session of a project
      that held the stricter minimum age, turned a part off, set
      inspection on a host the other project passes, or had a
      `deny_rules` entry, and see each kept as a held source, a 5019
      event that names the project, and the widening shown as pending,
      while the project's binding is no longer injected
      (S07-egress-gateway, "Session end");
    - deny a name, start and end another session, and see the name
      still refused with no new approval event until every session
      running at the deny has ended; answer an approval after a
      session started or ended, and see the answer refused and the
      request shown again (S07-egress-gateway, "Deny duration");
    - start a learn-mode session while a session or a debug shell of
      another project runs, and start another project's session while
      a learn-mode session runs, and see each refused with the other
      project named;
    - start another project or an untrusted repository in the
      isolated VM while a project set to the isolated slot has a
      session there, and the reverse, and see each refused;
  - send an attacker-supplied token in a credential parameter that a
    git host's profile names and get a `403`: GitLab's `private_token`
    in the query string, as `private%5Ftoken`, as `private_token[]`,
    repeated, empty, as a form-encoded body field, as a
    `multipart/form-data` part, as a top-level JSON key, and as a
    JSON-escaped key (`"private\u005ftoken"`); send a JSON body that
    doesn't parse, or one past the size cap with no `Content-Length`,
    and get a `403`; send a token on a GET and see it removed
    (X22-no-guest-credentials);
  - install a scoped npm package (`/@scope%2fname`) and read a GitLab
    project by its encoded path (`/api/v4/projects/group%2Fproject`),
    and see both pass; fetch `/@scope/name`, `/@scope%2fname` and
    `/@scope%2Fname` and get the same gate decision for each;
  - send a credential that is neither a placeholder nor a fixed value, and
    find a detection finding with its header name and no value;
  - fetch from an inspected host with a credential binding a response
    that sets a cookie and has `X-OAuth-Scopes`,
    `X-Accepted-OAuth-Scopes` and `X-OAuth-Client-Id` headers, and see
    none of them reach the guest and a log record with each name and
    no value;
  - turn off every GitHub part but `git` and see fetch and push to
    `github.com` pass, any REST or GraphQL request refused with
    `profile-part-off`, no GitHub binding injected on `api.github.com`,
    and a lookup of `api.github.com` refused at DNS with no approval
    event, also while another project's rule allows that host; load a
    trusted repository rule on that host, or on `api.github.com` with
    the path `/graphql`, `/graphql/`, `/graph*`, `/%67raphql` or `/**`
    and any method, and see a load error (S07-egress-gateway,
    "Profile parts");
  - with a boundary that forbids `api.github.com` and only the GitHub
    `graphql` part on, see the prover refuse the session start
    (S09-policy-credentials-audit, "Built-in profiles in the check");
  - download a Homebrew bottle from `ghcr.io` with the guest's
    `Authorization` removed and the anonymous binding injected, through
    the redirect to `pkg-containers.githubusercontent.com`; send any
    other method to `/v2/homebrew/core/` and see it refused; send
    `ghcr.io/token?scope=repository:homebrew/core/jq:pull,push` with
    guest Basic credentials and see it denied;
  - with a stand-in for `ghcr.io` that refuses the fixed value, see the
    token fallback fetch only the fixed `ghcr.io/token` URL when the
    401 names another realm, refuse a bottle name outside the OCI
    repository-name grammar, refuse requests past the per-VM rate
    with `anonymous-token-cap`, and not retry a 401 with a fresh token
    (S07-egress-gateway, "Token fallback");
  - request `/v2/homebrew/core/x/../../other/image/...` and
    `/v2/homebrew%2Fcore/...` on `ghcr.io`, and paths with `%zz`, a
    backslash, a `%2e%2e` segment, or a segment like `wget%2F..%2Fcurl`
    or `wget%2F%2e%2e%2Fcurl`, and see all refused with
    nothing sent upstream;
  - call the model API with an attacker-supplied key and observe that it
    is replaced;
  - send a credential placeholder outside its binding (another host, a
    query string, a body) and get a `403`;
  - look up a burst of unknown hostnames from the guest, and see the
    approval limits deny the requests past the limit and log each with
    the rule, with no more prover runs than the cap
    (S09-policy-credentials-audit);
  - once X14-flow-attribution has delivered labels: forge or omit a flow label and
    confirm only rules without program narrowing match, and replace a
    pinned binary and confirm its connections are denied;
  - download a package version younger than the minimum age, or with a
    known vulnerability, and find the too-young version missing from the
    registry metadata; do the same for Go with the checksum-database
    clock and `sum.golang.org` reached through the gate (I76);
  - fetch a gated registry path that fits no known form, and get a 403
    naming the rule; with a test double for the Go module proxy,
    redirect a download elsewhere and see it refused;
  - read or change policy, credentials, or the audit log from the guest;
  - as a project user outside the session profile, bind `wb-guestd`'s
    vsock port and see `EACCES`, since the guest kernel's rule for
    ports below 1024 is undocumented; inside a session, open an
    `AF_VSOCK` socket and see `EPERM` (X27-vsock-confinement);
  - write into the host repository through the git transport; fetch an
    object outside the session's branch by its ID; push outside
    `refs/heads/wb/<session-id>/`, or a tree with a `.git` entry;
  - check that the landing repository holds none of a refused push's
    objects: right after the push for each refusal of the pre-receive
    check (a ref too long for the file system, a stale old object ID,
    directory/file and case clashes, an object over the cap), and after
    the cleanup for a push that failed later (X26-pre-receive-check);
  - push each of these and see it refused before git unpacks it, with
    the memory of the git child and of `wb-hostd` measured
    (X26-pre-receive-check): a delta whose result, source size or own
    data length is over the per-object cap; a blob whose inflated size
    is over it; objects whose sizes add up to more than the per-push
    cap; a pack header that declares more objects than the count cap,
    with no entries after it; and an entry that declares a small size
    and is followed by hundreds of MiB of empty zlib stored blocks,
    refused on its wire bytes. Under the caps, the git child's peak
    memory stays near what X26-pre-receive-check measured, 207 MiB at a
    100 MiB per-object cap;
  - kill `receive-pack` in the middle of a push, and check that the
    cleanup leaves no `objects/tmp_objdir-*` and no `*.lock` under
    `refs/heads/wb/`;
  - forge an approval prompt via terminal output and confirm nothing
    treats it as one;
  - print every sequence the terminal filter drops, a guest window
    title, and a fake session summary in the background color, then
    exit: confirm the host terminal received none of the sequences,
    still shows the title `wb` set, and shows the real summary legibly.
- **Benchmarks** (NFR01-startup, NFR02-fs-speed): time to Claude prompt (warm, suspended).
  The suspended case restores a state saved after a Claude Code
  session and a build, not an idle guest, and records the state file's
  size (X02-warm-start). Also
  `npm ci` (with the dependency gate on), `git status` on a large
  repository, and an incremental build on the data disk compared with
  the host; throughput of a large download through the gateway, with
  the CPU time of `wb-netd` and `wb-proxyd`, compared with the figures
  of X03-network-path at the same MTU. Its CPU figures cover the
  `wb-netd` stand-in and a plain HTTP stand-in for `wb-proxyd` in one
  process, so they are a bound on the two together, not on `wb-netd`.
  The filesystem figures are reported against the NFR02-fs-speed goal
  with the host's conditions (load, FileVault, Endpoint Security
  agents, chip), and don't pass or fail the release gate. Results vary
  more between hosts than the gap they would judge.
- **Platform contract tests.** Each `internal/platform` interface has
  one test suite that every OS implementation runs (S12-platforms).
- **CI.** Go unit tests, lint, and vet run natively on macOS, Linux, and
  Windows hosted runners; Swift on macOS; fuzz smoke runs. The
  conformance suite and benchmarks need machines that can run the guest
  VMs (Apple Silicon for macOS guests; nested virtualization or bare
  metal elsewhere); hosted runners mostly cannot, so self-hosted runners
  are needed later.

## Spikes

The open questions to answer before building on them are in X00-index.
Each answered spike has a result page next to it, which X00-index links:
X02-warm-start (restoring a macOS guest from saved state),
X03-network-path (the guest network on gVisor's stack),
X05-fs-benchmark (the data disk against the host, and its disk settings),
X07-git-round-trip (the git transport between guest and host),
X18-vsock-handoff (vsock and descriptor hand-off on macOS),
X19-terminal-filter (the host terminal stream filter),
X21-dep-gate-registries (the dependency gate on real registries),
X22-no-guest-credentials (clients when guest credentials are removed),
X23-sandboxed-daemons (Go daemons confining themselves on macOS),
X24-openshell-artifacts (the OpenShell parts the specs reuse) and
X26-pre-receive-check (push checks before the quarantine lands).
What V1 leaves out is in V1-initial.

**Status:** Draft
