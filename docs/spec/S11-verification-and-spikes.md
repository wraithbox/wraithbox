# S11 - Verification and Open Questions

**Purpose:** How the requirements are proven, and which assumptions must
be tested before building on them.

## Test layers

- **Unit tests.** Go `go test -race`, table-driven; Swift Testing.
  The `wb-vmd` device-set check (S04-architecture, "The device set is
  fixed and checked") has a test that passes it each of a NAT network,
  a bridged network, a shared directory, a console or SPICE device, an
  audio device, a disk or serial port file outside the VM's bundle,
  and a second network device, and sees each refused with its rule,
  and that accepts the fixed set of S06-vm-lifecycle
  (X25-vmd-sandbox).
  Supervision (S04-architecture, "Supervision and failure") has tests
  that close each daemon's control channel and see `wb-netd` and
  `wb-proxyd` exit, `wb-launcher` end what it started and exit, and a
  fake VM provider stop its VMs. A test on a fake clock kills
  `wb-netd` five times in 10 minutes and sees the backoff, each new
  `wb-netd` start with the wildcard budget and the synthetic name
  mapping its predecessor left, a debit written just before the kill
  included, no restart after the fifth exit, and a session start then
  refused with `daemon-given-up`. The same test runs with the VM's
  audit event budget exceeded and its refusal events suppressed, and
  sees every debit and every mapping entry and release counted. An
  event write that fails with `ENOBUFS` or `EAGAIN` refuses new work
  with `event-channel-blocked`, and the record is written once the
  retry succeeds. A
  mapping entry with an uppercase or non-LDH name, a trailing dot, an
  address outside 198.18.0.0/15, or one past 131,072 live entries is
  logged with `mapping-entry-invalid` and ends the `wb-netd` that sent
  it, as a record received with `MSG_TRUNC` does. Entries are kept
  until their release record, with `wb-hostd`'s receipt time. A
  `wb-proxyd` restart gives every running VM a new hand-off socket,
  and a running `wb-netd` refuses a socket message with two
  descriptors or another VM's generation. An accounting write that blocks past
  100 ms refuses the lookup with `accounting-write-failed`. A
  `wb-guestd` that reports another build version, a version string
  that is not ASCII, longer than 64 bytes or not of the fixed form,
  and a host peer of another build version or of an unstamped build,
  are each refused before any other request, with the version escaped
  and cut in the log. A host peer from a replaced bundle makes
  `wb-hostd` exit with `bundle-replaced`, and an unreadable installed
  version doesn't make it exit. The type that holds
  `wb-hostd`'s copy of the packet transport descriptor has no read or
  write method, and
  the copy is closed at VM stop and at a restore.
  The audit writer has tests that exceed the per-class budget and see
  no 5019, 2004, approval, returned-work, store-access, VM or session
  record dropped, and `wb-netd` and `wb-proxyd` refuse with
  `audit-budget` until the window turns, and that stop a VM with an
  open run of suppressed refusals and see its count in the stop record
  (S09-policy-credentials-audit, "Audit"). A test on a fake volume
  drops the free space below the floor and sees the VM whose data disk
  grew stopped before any write fails, and the reserve allocated again
  once space returns.
  Leaf issuance in `wb-proxyd` (S09-policy-credentials-audit, "TLS
  inspection certificate authority") has a test that asks for a leaf
  for a host the policy doesn't inspect, a host in pass mode, a stream
  whose SNI differs from its hostname, a hostname on another VM's
  stream, and a VM whose only CA has expired. In every case
  `wb-proxyd` doesn't sign a leaf. The pass-mode stream is relayed
  unchanged, and each other stream is reset and logged with its rule. The CA rotation has a
  test on a fake clock: day 30 issues and installs the successor, day
  58 switches only after the install is recorded and otherwise refuses
  the VM's streams with `ca-not-installed`, and day 60 removes the old
  CA and destroys its key. A restore test and a host wake test check
  the order: clock set, expired CAs removed, the current CAs installed
  from the host's record, and only then streams accepted, signed with
  the CA `wb-proxyd` signed with before the save or the sleep. A save
  on day 40 and a restore on day 45 keep signing with the old CA, and
  a host that sleeps from day 50 to day 61 removes the old CA before
  it accepts a stream. Each step checks its 5019
  event.
  The stream hand-off in `wb-proxyd` (S07-egress-gateway, "Stream
  hand-off") has a test with two VMs at different generations. On VM
  A's hand-off socket it sends these streams:
  - one whose generation field is VM B's;
  - one to an address only VM B's copy holds, and one to an address
    with no entry;
  - one on a port the name's policy doesn't allow;
  - one to a name that a policy change removed after its report;
  - one with no descriptor, one with two, and one with three, which
    cuts the control data (`MSG_CTRUNC`);
  - one with a datagram socket, and one with a socket that isn't a
    Unix domain socket, as the stream;
  - one sent right behind a report that waits on `wb-hostd` for the
    wildcard budget, which passes once the report is accepted;
  - one in a message over 4 KiB;
  - streams past the per-socket cap, while VM B's streams still pass.

  On the same socket it sends name reports with a name only VM B's
  policy allows, a name that fails the name form rule, a name with a
  capital letter or a trailing dot, an address outside the synthetic
  range, and new names under a wildcard past the wildcard budget. It
  also reports an address held for another name twice, once with no
  stream open and once with a stream to it open, and sees the old
  entry removed and the open stream closed. Each is refused and logged
  with its rule and check, and `wb-proxyd` doesn't resolve a name
  upstream, open an upstream connection or fetch a binding. The
  malformed ones, a name not in canonical form and an address outside
  the range, also close VM A's hand-off socket, and `wb-hostd` ends
  VM A's `wb-netd` and counts the exit. A replay
  of the live entries doesn't change the copy or the wildcard
  count, and a wildcard name whose accounting record blocks for more
  than 100 ms is refused. From `wb-hostd` the test sends hand-off messages
  with an older generation, a VM that isn't a VM slot, and a datagram
  with two descriptors, and sees each refused. An equal generation
  replaces the old socket and closes its streams. A change to the
  effective policy reaches `wb-netd` only after `wb-proxyd` has
  acknowledged it.
  The policy loader has a test that puts a host in pass mode from a
  repository's `.wraithbox/policy.yaml` with `config_trust` on, on a host with a
  credential binding, on a host a built-in profile covers, and without
  a reason, with a wildcard host, and then adds a binding on a pass
  host, and sees each refused at load with an error that names the
  host and the rule (S07-egress-gateway, "Modes, per host"). The same
  test loads a policy with a host that has `_` or a non-ASCII byte in
  its name, and runs `wb allow` on such a host, and sees both refused
  with the rule `dns-name-form` (S07-egress-gateway, "Packet path",
  DNS). A global
  pass entry on a host that a joining project's remote names refuses
  that project's session start, with the project named.
- **Fuzzing.** Go native fuzz targets for every parser that sees
  guest-controlled bytes: Ethernet/IP/TCP handling at the link endpoint,
  DHCP, DNS, TLS ClientHello parsing (a ClientHello split across
  TLS records included), the first-byte protocol check of
  S07-egress-gateway ("Non-HTTP streams"), HTTP/1.1 and HTTP/2 request
  handling, request path canonicalization, the credential parameter
  scan of query strings and of form and JSON bodies
  (X22-no-guest-credentials), policy matching, the git transport framing (the request
  header and the push command list, X07-git-round-trip), the pack
  scanner in front of `receive-pack` and the input of the pre-receive
  check (X26-pre-receive-check), the accounting records (debits,
  mapping entries and releases) and event records
  `wb-hostd` reads from `wb-netd` and `wb-proxyd` (S04-architecture,
  "Accounting and event channels"), every vsock RPC
  handler in `wb-hostd`, the stream framing that multiplexes guest
  requests on a host-opened connection, with the project each request
  is attributed to (S04-architecture, B29-vsock-handoff), the name
  reports, releases and stream messages `wb-proxyd` reads from `wb-netd` and the
  hand-off messages it reads from `wb-hostd` (S07-egress-gateway,
  "Stream hand-off"), network policy YAML, the guest flow labels
  of S13-guest-confinement, and the dependency gate's parsers: npm
  request paths, PyPI distribution file names, Go escaped module paths
  and versions, crates.io index lines, and `sum.golang.org` lookup
  responses, and the toolchain manifest parser in `wb-hostd` and the
  name check in `wb-guestd` (S06-vm-lifecycle, "Toolchain prefix")
  (Swift: property-based tests where fuzzing is impractical).
  The terminal filter in `wb` (S16-terminal-stream) is one of
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
    matches a mark rule of S16-terminal-stream;
  - the filtered output followed by the exit reset, replayed into a
    headless terminal emulator, leaves it in its initial state: modes,
    kitty keyboard stacks on both screens, keypad mode, character sets,
    scroll region, tab stops, and colors and style. For each of these
    states, a test removes its step from the reset and sees the
    property fail, for example `ESC =` with no `ESC >`.

  The pack scanner's target checks that what it forwards equals its
  input when a pack passes, and is a prefix of it when one is refused.
  A refused prefix never holds the pack's checksum (as long as the
  landing repository's hash), and the scanner's heap stays under a
  fixed bound plus its slice of entry sizes, which the object count
  cap bounds (X28-git-alloc-limit). Its seeds include REF_DELTA entries, delta chains, empty zlib stored
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
  - list the guest's network interfaces and routes: exactly one
    Ethernet interface, its lease from `wb-netd`, and no address or
    route in a vmnet subnet, which a NAT device would add
    (X25-vmd-sandbox; the spike's NAT guest got 192.168.64.4);
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
  - send SSH, a PostgreSQL startup message and a cleartext HTTP/2
    preface to an allowed TLS port, and SSH to port 22 of a git
    forge; open a stream to an allowed port and don't send; send an
    HTTP `CONNECT` and an `Upgrade: h2c` to an inspected host; send a
    ClientHello with an `encrypted_client_hello` extension to a pass
    host. See each reset or refused with its rule from
    S07-egress-gateway, "Non-HTTP streams", and no connection opened
    upstream for a stream reset on its first bytes;
  - send a ClientHello with a real ECH configuration to an inspected
    host and see the client abort and `tls-ech-refused` logged; after
    a HelloRetryRequest from a pass host, send a second ClientHello
    with another SNI and see it reset;
  - try to add a raw TCP entry on a guarded host (`github.com`,
    `ssh.github.com`, a host with a credential binding), at load and
    at a recompute, and see it refused with the host named;
  - with an allowed name that resolves to `127.0.0.1`, an RFC 1918
    address, `169.254.169.254` or an IPv4-mapped form of one, open an
    inspected, a pass and a raw TCP stream to it, and see each refused
    with `upstream-address-refused` and no upstream connection. Do the
    same for a name that resolves to an address of one of the host's
    interfaces, to an address in an on-link prefix of the host (a
    global IPv6 LAN address included), to `192.0.0.8`, and to
    IPv4-compatible, 6to4, and Teredo addresses. Load a policy whose
    host is `127.0.0.1` or `0x7f000001` and see it refused;
  - to a pass host, send client application data, or a handshake
    record that isn't a ClientHello, before the server's ServerHello,
    and see it reset;
  - push to a repository outside the project set, with and without an
    attacker-supplied token; create a gist or repository; publish a
    package. The project set is the VM's: the repositories of every
    project with a session in the VM (S07-egress-gateway, "Enforced per
    VM");
  - in a learn-mode session, resolve a name that isn't allowlisted and
    see `NXDOMAIN` with the same answer bytes as outside learn mode, no
    approval event, and the name in `wb learn report`; push to a
    repository outside the project set and see it refused; resolve
    more distinct names than the list cap and see the rest refused with
    `learn-list-full`, not listed, and counted as dropped in the
    summary; resolve a name with a control byte, an escape or a
    non-ASCII byte and see it refused with `dns-name-form` and absent
    from the report, the summary and `wb status`; put a name with a
    risk-check finding on the list, approve the batch, and see only
    the names without a finding allowed (S07-egress-gateway,
    "Approvals and learning");
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
    - start another project, or an `--isolated` session, in the
      isolated VM while a project with `placement = isolated` has a
      session there, and the reverse, and see each refused;
    - start `--learn` in a project with `placement = isolated`, and
      with `--isolated` in a project with `placement = work` and
      `config_trust` on, and see each refused;
    - change a remote URL of a registered project's repository, start
      a session and see it refused with both URLs named, then run
      `wb project confirm` and see the next start pass, `config_trust`
      cleared, and a 5019 event with both URL sets; switch the remote
      between its SSH and HTTPS forms and see the start pass; start a
      session in a second `git worktree` of the repository and see it
      use the same project (S05-cli, "Naming");
    - in a repository without a remote, replace it at the same path
      with another repository without a remote and see the start
      refused with both root commit sets named;
    - move project A from `/x` to `/y` with `wb project move`, clone
      another repository into `/x`, start a session there, and see a
      new project with a new id, none of A's settings, policy or
      approvals, and A still at `/y`;
    - move a project's directory without `wb project move`, start a
      session at the new path, and see it refused with the old project
      named and `wb project move` and `wb project rm` offered; clone
      the same repository a second time while the first clone stays,
      and see the second registered as its own project, with a notice
      that names the first; do the same for a project with
      `placement = isolated` and see the second clone start isolated,
      with none of the first's approvals or `config_trust`;
    - register a repository with no remote and no commit, commit,
      start a session and see the root commit recorded with a 5019
      event, then replace the repository and see the start refused;
    - start a session in a directory whose `.git` file reads
      `gitdir: <project A>/.git`, in one whose `.git` file reads
      `gitdir: <project A>/.git/modules/sub`, and in one with
      `GIT_DIR`, `GIT_CONFIG_PARAMETERS` or `GIT_CONFIG_COUNT` set in
      the environment to point at project A or rewrite its
      configuration. See the first two refused with the reason and
      nothing registered, and the third resolve to the directory's own
      repository, not A; run `wb trust` and `wb project place` in the
      first two and see them refused the same way;
    - run `wb project rm` with the VM stopped, restart `wb-hostd`, start
      the VM and see the guest user removal sent; send a removal reply
      from the guest for another project and see that project's record
      kept;
    - on macOS, start sessions in one repository as `~/git/foo` and
      `~/Git/foo` and through a symbolic link, and see one project;
    - run `wb project place isolated` for a project that has a home on
      a data disk and see it refused with I44 named, and while the
      project has a session and see it refused with the session named;
      run it in a new clone before its first session and see the first
      session start in the isolated VM;
    - run `wb trust`, `wb untrust`, and each changing `wb project`
      command, and see a 5019 event with the values before and after;
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
    repository configuration rule on that host, or on `api.github.com` with
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
  - as a project user outside the session profile, bind a free vsock
    port below 1024 and see `EACCES`, and bind `wb-guestd`'s port while
    it runs and see the bind refused. The rule for ports below 1024
    isn't in Apple's developer documentation, only in the XNU source
    (`bsd/kern/vsock_domain.c`, `VSOCK_PORT_RESERVED`, `proc_suser`),
    so each guest image is checked. Inside a session, open an
    `AF_VSOCK` socket and see `EPERM` (X27-vsock-confinement);
  - as root, run `sudo -l -U` for each project user and see no rules
    listed, and see no `admin` or `wheel` group in its `id`
    (S06-vm-lifecycle, X27-vsock-confinement);
  - as a project user, replace a link in the toolchain prefix's `bin`,
    write to a binary in its `Cellar`, and run `brew install`, and see
    each refused; as root, check that every file in the prefix belongs
    to the toolchain user and to its own group, that the group has no
    other members, and that others can't write any of them; hand
    `wb-hostd` manifests with a `tap`, a `cask`, a `system` line,
    `brew "--force"`, `brew "--HEAD"`, `brew "jq.rb"`, a name with a
    `/`, a name over the length cap and too many lines, and see each
    refused with the line and the rule, and send the same names to
    `wb-guestd` directly and see it refuse them too; declare a formula
    with no bottle for the guest, and one whose dependency has none,
    and see the session refused with the formula's name before
    anything is installed; remove the formula data snapshot and see
    the reconcile refused (S06-vm-lifecycle, "Toolchain prefix",
    X20-shared-homebrew);
  - with a session of project A running, reconcile project B, whose
    formulae share dependencies with A's, and see every `opt/*` link
    of A's formulae point where it did before; run A's tools with the
    session `PATH`, which leaves out the prefix's shared `bin`; and
    see Homebrew's temporary and cache files only in the toolchain
    user's mode-700 home (S06-vm-lifecycle, "Toolchain prefix", I180);
  - write into the host repository through the git transport; fetch an
    object outside the session's branch by its ID; push outside
    `refs/heads/wb/<session-id>/`, or a tree with a `.git` entry;
  - check that the landing repository holds none of a refused push's
    objects: right after the push for each refusal of the pre-receive
    check (a ref too long for the file system, a stale old object ID,
    directory/file and case clashes, an object over the cap), and after
    the cleanup for a push that failed later (X26-pre-receive-check);
  - push each of these and see the scanner refuse it, with its rule,
    before the pack's checksum reaches git and so before git resolves
    any delta, with the memory of the git child and of `wb-hostd`
    measured (X26-pre-receive-check, X28-git-alloc-limit): a delta whose
    result, source size or own data length is over the per-object cap;
    a blob whose inflated size is over it; objects whose sizes add up to
    more than the per-push cap; REF_DELTA entries whose declared source
    sizes alone add up to more than the per-push cap; 10,001 REF_DELTA
    entries against distinct small bases in `export.git`, refused
    before git reads a base; a pack header that declares more objects
    than the count cap, with no entries after it; and an entry that
    declares a size of 1 byte, followed by empty zlib stored blocks
    until the pack is one byte over the 1 GiB wire-byte cap, which the
    scanner refuses on that cap while its memory doesn't grow. Under the
    caps, the git child's peak memory stays near what
    X26-pre-receive-check and X28-git-alloc-limit measured, 207 MiB at a
    100 MiB per-object cap;
  - push one tree of 100 MiB, and one commit with 100 MiB of `parent`
    lines, each under the caps, and record the git child's peak memory
    when the conformance suite is built. These cases have no measured
    value yet (S08-workspace-and-git, "Bounds");
  - with the scanner off and `receive.unpackLimit=1` as S08 sets it, push a pack one byte over 1 GiB and see git
    refuse it on `receive.maxInputSize`;
  - check that the environment builder of `wb-hostd` sets
    `GIT_ALLOC_LIMIT=104857601` for `receive-pack`, in a unit test;
  - push with a first command that doesn't ask for `side-band-64k`,
    and see the ref filter refuse it. With a pack that makes
    `receive-pack` write escape sequences and more than 4 KiB to its
    standard error, check that the log holds one quoted field of at
    most 4 KiB with none of them;
  - with the scanner off and `receive.unpackLimit=1` as S08 sets it, push the same packs and see git refuse each
    one that asks for more than the per-object cap in one allocation,
    with the git child under 20 MiB, through `GIT_ALLOC_LIMIT`
    (X28-git-alloc-limit). An object of exactly the cap passes and one
    byte more is refused. A pack header that declares 1,638,400 objects
    or more is refused;
  - with the scanner off and `receive.unpackLimit=1` as S08 sets it, push 64 deltas under the per-object cap
    without the pack's checksum, and see `index-pack` fail with no
    delta resolved, in under a second (X28-git-alloc-limit);
  - with the scanner off and `receive.unpackLimit=1`, push a pack that
    names more than 4.2 million distinct object IDs (for example trees
    that name many blobs) and see git fail when its object hash table
    would grow past `GIT_ALLOC_LIMIT` (S08-workspace-and-git, "Bounds");
  - record the wall-clock time of the slowest pushes the caps allow,
    against the 5 minute watchdog: 1 GiB of empty zlib stored blocks
    behind one entry, 1 GiB of trees under `--strict`, and 1 GiB of
    bases that `--fix-thin` copies in from `export.git`, each just
    under its cap. Each one must finish well inside the watchdog;
  - start `wb-hostd` with a stand-in git that ignores
    `GIT_ALLOC_LIMIT` and exits with 0, one that exits with 128 and
    another message, and one that hangs past the 5 s timeout, and see
    every push refused with the reason;
  - the git data scope of S08-workspace-and-git (B41-git-data-scope),
    each refusal with its rule in the audit log:
    - start a session with an untracked file that holds a token the
      scanner knows, and a tracked binary file changed to hold one, and
      see the untracked file absent from the guest, the tracked file
      at its `HEAD` version, and both listed in the summary;
    - start a session with an untracked symbolic link to a host file,
      and see a link in the guest whose target is the string only,
      with none of the file's content;
    - give the host repository remotes with a token in the user info,
      an `ext::` URL, a `git+ssh://` URL, an IP-literal host and a URL
      that doesn't parse, and see the guest get only cleaned `https://`
      URLs, the rest listed as withheld;
    - start `wb` and `wb-hostd` with a stand-in git that reports a
      version below the minimum, and one that reports a release
      candidate of the minimum, and see both refused with the reason;
    - fill `landing.git` until its size plus the worst case of one push
      is over the cap, and see the next push refused before
      `receive-pack` starts;
    - remove a ref from the selection, start a session, and see a fetch
      of that ref's tip commit by ID refused, and the ref not
      advertised;
  - kill `receive-pack` in the middle of a push, and check that the
    cleanup leaves no `objects/tmp_objdir-*` and no `*.lock` under
    `refs/heads/wb/`;
  - forge an approval prompt via terminal output and confirm nothing
    treats it as one;
  - print every sequence the terminal filter drops, a guest window
    title, and a fake session summary in the background color, then
    exit: confirm the host terminal received none of the sequences,
    still shows the title `wb` set, and shows the real summary legibly;
  - the guest admin password at first contact (S15-least-privilege),
    with a data disk whose project homes hold a LaunchAgent and a login
    item: see the rotation finish before `wb-hostd` sends the time and
    before `wb-guestd` lists its sessions, nothing from the data disk
    run before it, `wbadmin` disabled again with a usable secure token,
    and the image password refused by `dscl . -authonly`. Drop the link
    during a rotation, and see the disk marked "rotation unknown" and
    every session start refused with its rule;
  - the session lifecycle (S06-vm-lifecycle, "Session lifecycle"),
    with a `wb-guestd` that the test drives as guest root. Each case
    checks the audit entry with its rule:
    - report an exit code of 256 and a signal number of 0 and of 65,
      and see `wb` exit with 255;
    - drop the link, then list a running session of the other VM, an
      ended session, a malformed id, an unknown id and a running
      session of an earlier VM generation, and see "end" for each and
      no session of the other VM change state;
    - drop the link and leave a running session out of the list, and
      see it lost and `wb` exit with 255;
    - send a list of 65 entries, and see it refused and the connection
      closed; drop the link again and again, and see at most 20
      connections to the VM in a minute;
    - drop the link while the WIP commit runs, bring it back, and see
      the stopped commit killed, the commit run again and the session
      end normally;
    - list one id both as a held session and as a report, and see the
      list refused; send a report for another VM's session and for an
      unknown id, and see "end" and the report gone from the guest's
      directory; send a report again for an ended session, and see it
      acknowledged with no change;
    - report a WIP push that never reached `receive-pack`, and see a
      failed push with `wb` exiting 255 and the summary's count taken
      from `landing.git`;
    - drop the link right after a session ends, before its report
      reaches the host, and see the session end with that report, not
      as lost;
    - never report a WIP commit, and see the session end as a failure
      at its deadline, with `wb` exiting 255 and the summary naming
      the worktree's path and `wb shell`;
    - run `wb vm stop` and `wb vm suspend` with a running session,
      and see both refused with the session named;
    - during recovery, push to the recovering session's branch and to
      another session's, and see only the first accepted;
    - push to an ended session's branch, and to a discarded one's, and
      see both refused;
    - close the terminal while a child of `claude` has left the
      session with `setsid` and another session of the project runs,
      and see the child logged as a survivor; with no other session,
      see it killed.
- **Benchmarks** (NFR01-startup, NFR02-fs-speed): time to Claude prompt (warm, suspended, cold boot).
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
  The startup and filesystem figures are reported against the
  NFR01-startup and NFR02-fs-speed goals with the host's conditions
  (load, FileVault, Endpoint Security agents, chip), the guest's memory
  size, and whether the state file was in the host's page cache. They
  don't pass or fail the release gate (decided on I19 and I111).
  Filesystem results vary more between hosts than the gap they would
  judge.
- **Platform contract tests.** Each `internal/platform` interface has
  one test suite that every OS implementation runs (S12-platforms).
- **CI.** Go unit tests, lint, and vet run natively on macOS, Linux, and
  Windows hosted runners; Swift on macOS; fuzz smoke runs. The
  conformance suite and benchmarks need machines that can run the guest
  VMs (Apple Silicon for macOS guests; nested virtualization or bare
  metal elsewhere). Hosted runners mostly cannot. Until v1 is close,
  tests that need a running macOS guest (spikes, the conformance suite,
  benchmarks) run on the maintainer's Mac, outside `ci`. Agents may
  build issues that need a guest: the `needs-vm` label marks them, and
  a wave runs at most one such builder at a time. That keeps the Mac
  within NFR05-two-macos-vms (`docs/agents/orchestration.md`). The
  runner question is revisited near v1 (decided on I50).

## Spikes

The open questions to answer before building on them are in X00-index.
Each answered spike has a result page next to it, which X00-index links:
X02-warm-start (restoring a macOS guest from saved state),
X03-network-path (the guest network on gVisor's stack),
X05-fs-benchmark (the data disk against the host, and its disk settings),
X07-git-round-trip (the git transport between guest and host),
X18-vsock-handoff (vsock and descriptor hand-off on macOS),
X19-terminal-filter (the host terminal stream filter),
X20-shared-homebrew (one Homebrew prefix for every project user),
X21-dep-gate-registries (the dependency gate on real registries),
X22-no-guest-credentials (clients when guest credentials are removed),
X23-sandboxed-daemons (Go daemons confining themselves on macOS),
X24-openshell-artifacts (the OpenShell parts the specs reuse),
X26-pre-receive-check (push checks before the quarantine lands) and
X28-git-alloc-limit (git's allocation cap against delta bombs).
What V1 leaves out is in V1-initial.

**Status:** Draft
