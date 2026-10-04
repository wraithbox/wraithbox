# 007 - Egress Gateway

**Purpose:** The only path from a guest to any network: a userspace
network stack (`wb-netd`) and a policy-enforcing proxy (`wb-proxyd`) on
the host.

**Requirements:** F8-no-proxy-config to F10-learn-mode, S4-no-guest-secrets to S7-dep-gate, S10-audit, S11-root-gains-nothing, N6-explained-refusals.

## Packet path (`wb-netd`)

- **Packet transport.** Each VM's virtual NIC is connected to its
  `wb-netd` by a platform-specific packet transport (spec 012) that
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
    event (F9-approve-unknown). After approval, the next lookup succeeds.
  - Wildcard allowlist entries are bounded: each session may resolve at
    most a fixed number of new names under wildcards, and failed lookups
    count against that budget, because names themselves can carry data
    (R2-dns-names). Query rate and name length are limited.
- **Connections.**
  - TCP to a synthetic address on an allowed port is accepted by the
    stack and handed to `wb-proxyd` as a byte stream over a Unix socket,
    tagged with VM, project, session, hostname, and port.
  - TCP to any other address is reset; UDP other than DNS is dropped
    (clients fall back from QUIC to TCP); ICMP is answered only for the
    gateway address. The host, the LAN, and raw IP destinations are
    therefore unreachable by construction (S5-default-deny).

## Stream path (`wb-proxyd`)

- **Name binding.** The hostname comes from the synthetic address. For
  TLS, the ClientHello SNI must equal that hostname or the stream is
  reset. `wb-proxyd` resolves the real upstream address itself.
- **Modes, per host:**
  - **inspect** (default): terminate TLS with a leaf certificate from the
    Wraith Box CA (spec 009), apply HTTP policy and credential
    replacement, then open a new TLS connection upstream validated
    against the host's system trust store. HTTP/1.1 and HTTP/2.
  - **pass**: relay TLS bytes unchanged to the named host. Only for hosts
    where inspection breaks the client; every pass-through host is a
    documented residual channel, because the proxy cannot see what is
    sent (R1-allowed-channels).
  - Plain HTTP is allowed only when policy names `host:80`, and is
    always inspected.
- **Credential replacement** (S4-no-guest-secrets). On inspected hosts, credentials sent
  by the guest (`Authorization`, `Proxy-Authorization`, API-key headers,
  cookies configured per binding) are always removed. If the host has a
  credential binding, the real credential is injected from the
  platform's secret store (spec 009).
  A token supplied by an attacker is therefore never forwarded, and the
  guest only ever holds placeholders.
- **Placeholder binding.** Each placeholder is bound to the hosts,
  ports, and paths of its binding, following OpenShell's provider
  model. A placeholder found anywhere else in a request (another host,
  a query string, a body) gets a `403` naming the binding, and an audit
  event. The placeholder itself is never sent upstream. For AWS,
  `wb-proxyd` signs requests (SigV4) rather than injecting a key.
- **HTTP policy** (S6-repo-writes). Rules match method and path per host, GraphQL
  operation type and name, and WebSocket messages, in the OpenShell
  policy schema (spec 009). Each inspected host enforces its rules by
  default. A host can be set to `audit` while a new rule is tried out:
  violations are then logged but allowed. Built-in profiles:
  - *git hosting*: reads allowed; `git-receive-pack` and mutating API
    calls only for the project's repositories (derived from the host
    repository's remotes, plus explicit additions); gists, repository
    creation and forks denied. GraphQL mutations are denied unless the
    operation name is allowlisted.
  - *model API*: the endpoints Claude Code needs, with the model
    credential binding.
  - *package registries* (npm, PyPI, Go module proxy, crates.io,
    Homebrew bottles): metadata and downloads only; publish and upload
    endpoints denied.
- **Dependency gate** (S7-dep-gate). For registry downloads, `wb-proxyd` looks up
  the requested version's publish time and known vulnerabilities (OSV
  data). Versions younger than the minimum age (default 7 days) or with a
  known vulnerability at or above the threshold (default HIGH) are
  refused with an explanatory error. If the lookup fails, the request is
  refused (fail closed); per-project overrides are explicit and audited.
  The gate implements OpenShell's supervisor middleware API
  (`SupervisorMiddleware`, RFC 0009) and runs after policy allows a
  request and before credentials are injected, so it never sees a
  credential. It runs inside `wb-proxyd`.

## Approvals and learning

- An unknown destination produces an approval event in `wb-hostd`, shown
  as a native notification and listed by `wb status`: *allow for this
  session*, *allow for this project*, or *deny*. Each request shows the
  result of the prover's risk check on the rule it would add (spec 009),
  such as new reach for a credential or a new write method. Policy
  changes take effect without restarting anything.
- **Learn mode** (trusted projects only, F10-learn-mode): DNS resolves any name and
  connections are inspected and allowed, while credentials stay host-side
  as usual. The session's destinations become a suggested allowlist for
  `wb learn report`.

## Performance

Packets are processed in userspace, so throughput is lower than kernel
networking. Spec 011 sets a benchmark for large downloads. If throughput
misses it, the fix is inside `wb-netd` (batching, buffer sizes), not a
second network path.

**Status:** Draft
