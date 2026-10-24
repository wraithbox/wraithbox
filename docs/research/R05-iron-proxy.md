# R05 - iron-proxy

**Purpose:** What iron-proxy is, how it relates to Wraith Box's egress
gateway, which of its threats and defenses Wraith Box lacks, and what
to do about it. Read at iron-proxy commit `60ed3566` (v0.54.0,
2026-10-21).

**Requirements:** SEC04-no-guest-secrets, SEC05-default-deny,
SEC06-repo-writes, SEC10-audit, NFR08-licensing,
T01-allowed-channels, T10-unnamed-credentials

## What iron-proxy is

iron-proxy (`ironsh/iron-proxy`, Apache-2.0, v0.1.0 on 2026-04-01,
v0.54.0 on 2026-10-21) is an egress proxy that inspects TLS, in one Go
binary with one YAML file. It is for CI jobs, containers and coding
agents. Its own CI runs behind it, through the GitHub Action
`ironsh/iron-proxy-action`. One author wrote most of it. The company
iron.sh sells a control plane on top: in managed mode the proxy polls
it for rules, secrets and transforms (`internal/controlplane`).

The binary has these parts:

- **A DNS server** that answers every `A` query with the proxy's own
  address. Names that match a `passthrough` glob resolve for real and
  bypass the proxy, and static records come first.
- **HTTP and HTTPS listeners.** The HTTPS listener terminates TLS with
  leaf certificates from a CA whose key the operator supplies as a
  file. A global `sni-only` mode relays TLS by its SNI instead. An
  optional tunnel listener accepts `CONNECT`, SOCKS5 and absolute-form
  HTTP.
- **A transform pipeline**, run in order on each request:
  - `allowlist`: domain globs and CIDRs, with optional method and path
    rules. Anything else gets a 403.
  - `secrets`: swaps a proxy token for a real secret, or injects one
    (see "Credentials" below).
  - `header_allowlist`, `annotate` and `body_capture`.
  - `judge`: an LLM that can only refuse a request its rules match.
  - `grpc`: calls an external `TransformService`
    (`proto/transform/v1/transform.proto`).
  - credential transforms: `aws_auth` re-signs SigV4, `gcp_auth` and
    `gcp_id_token` get Google tokens, `oauth_token` runs four OAuth 2
    grants, and `hmac_sign` signs requests for exchange APIs.
- **An upstream deny list**, checked against each address at connect
  time (`internal/dnsguard`).
- **MCP policy and an MCP gateway** for MCP's Streamable HTTP
  transport, a PostgreSQL proxy that pins a role per database, a
  response retry that an outside handler authorizes (its default
  header is `Payment-Receipt`), and a reload API.
- **Audit**: a JSON line per request with each transform's decision,
  and the same record as OpenTelemetry logs over OTLP.

iron-proxy doesn't confine the workload. Getting its traffic to the
proxy is the operator's job: DNS alone, which the README calls "easy to
bypass", nftables rules that the workload's container loads before it
drops `CAP_NET_ADMIN`, or TPROXY. In the usual Docker setup the proxy
and the workload share a kernel.

It has no written threat model. The README's problem statement names
the threats: a compromised dependency, a prompt injection or a
malicious build step that steals secrets, phones home or opens a
reverse shell. `AGENTS.md` lists the invariants a change must not
weaken: default-deny, the upstream deny list, secret values never in
logs, errors or responses, an audit record for every request, cleanup
on error, and the MCP and PostgreSQL gates. It has no list of accepted
risks.

## How it relates to Wraith Box

| | iron-proxy | Wraith Box V1 |
|---|---|---|
| What it is | An egress proxy | A VM per trust level, with an egress gateway on the host |
| Confinement | None of its own. The operator routes traffic to it | The VM, and `wb-netd` as its only network (SEC01-separate-kernel, SEC05-default-deny) |
| Name to destination | Every name answers with the proxy's address. `Host` or SNI picks the upstream | A synthetic address per allowed name, and the stream is bound to that name (S07-07-hosts-and-modes) |
| DNS | Every name. `passthrough` names resolve for real | Allowed names only, with a wildcard budget (S07-03-dns) |
| TLS | Inspect every host, or relay every host by SNI | Inspect by default. `pass` per named host, with a reason and ECH checks (S07-16-tls-and-tunnels) |
| Refused upstream addresses | Instance metadata addresses and loopback, by default | Private, link-local, CGNAT, unique local, embedded IPv4, and the host's own networks (S07-07-hosts-and-modes) |
| Policy | One YAML file per proxy | OpenShell's schema, per VM, built-in profiles, and the prover's boundary check (S09-policy-credentials-audit) |
| Guest's own credentials | Forwarded, unless a `header_allowlist` drops them | Removed on every inspected host (S07-08-requests-and-credentials) |
| Credential injection | Replaces a token wherever it is told to look, or sets a header or query parameter | Sets the binding's header. A placeholder anywhere else is refused (S07-09-placeholder-binding) |
| Secret sources | Environment, file, AWS, Vault, 1Password | The platform's secret store (S09-05-credentials) |
| CA key | A file | A Secure Enclave key per CA (S09-06-ca-keys) |
| Content checks | An LLM judge, MCP tool rules, external gRPC transforms | The dependency gate, through OpenShell's middleware API (S07-12-dependency-gate) |
| Audit | JSON and OTLP, request bodies when asked | OCSF 1.8.0 JSONL, never bodies or credential values (S09-11-audit) |
| Users | Operators of CI and container fleets | One developer on their own machine |

**Overlap.** iron-proxy does the job of `wb-proxyd`, and the DNS half
of `wb-netd`. Since it has nothing for the VM, the git round trip,
approvals or the dependency gate, it doesn't compete with
`wb claude`. A user who puts it in front of a container that runs
Claude Code gets egress control and credential replacement, but
shares a kernel with the agent and relies on the container's firewall
to keep traffic on the proxy.
iron-proxy is a component. It competes with `wb-proxyd` as a choice
between building and reusing. That choice is open now, because
`wb-proxyd` is still a stub that V1-M3-network-floor builds.

**Could `wb-proxyd` be iron-proxy?** Not without rewriting most of it.

- *Listeners.* `wb-proxyd` takes streams from `wb-netd` over a hand-off
  socket per VM, each bound to a name (S07-05-stream-hand-off).
  iron-proxy listens on TCP ports and takes the name from the request.
- *Policy.* One global file, against per-VM unions of OpenShell
  policy, guarded hosts, modes per host, profile parts and the
  prover.
- *Credentials.* The secret model differs at its root ("Credentials"
  below), and the default deny list is narrower.
- *Quality gates.* The tree has no fuzz targets. S10-tech-stack asks
  for one on every parser that handles guest bytes.
- *Licenses.* iron-proxy's own code is Apache-2.0, but its module
  depends on `hashicorp/golang-lru/v2` and `hashicorp/vault/api`, both
  MPL-2.0. S10-01-libraries doesn't allow copyleft, so the module
  can't be a dependency (NFR08-licensing). A package that doesn't
  import them, such as `internal/dnsguard` or `internal/mcp`, could be copied
  with its license notice. Its other dependencies include the AWS
  SDK, two 1Password SDKs (one a fork through a `replace` line), a
  PostgreSQL parser, OpenTelemetry and gRPC.

Besides a few packages and tests that could be copied, what is left
to reuse is the parts that Go's standard library already gives
`wb-proxyd`: TLS termination, the leaf cache and the upstream
transport.

**Shared ideas.** iron-proxy's `TransformService` and OpenShell's
supervisor middleware, which the dependency gate implements in process
(S07-12-dependency-gate), both let outside code allow, refuse or
rewrite a request. Each project speaks only its own proto.

**Where the two meet today.** Nowhere at run time. No spec gives
`wb-proxyd` an upstream proxy, so it can't send its traffic through an
iron-proxy. That is just as well: a second inspecting proxy would
be a second holder of credentials. Wraith Box's own CI could run
behind `ironsh/iron-proxy-action`, as iron-proxy's CI does. That is a
question about this repository's supply chain, not the product, and
the action would need an allowlist entry (`docs/github-settings.md`).

## Credentials

The difference that matters most is how each one puts a credential
into a request.

iron-proxy's `secrets` transform works in one of these modes:

- `inject` sets one header or query parameter to the secret on
  requests that match the entry's rules.
- `replace` looks for the proxy token in the headers it names (all
  headers when the list is empty), and optionally in the query, the
  path and the body. It swaps every occurrence for the secret with
  a substring replace (`internal/transform/secrets/secrets.go`).

With `replace`, the workload decides where in the request the secret
goes. On a bound host that echoes or stores what it receives, the
workload can read the secret back. iron-proxy's own demo does that on purpose: it
sends the token to `httpbin.org/headers`, which echoes the request's
headers, and tells the reader to "look for the real key in the
response" (`examples/docker-compose/client.sh`). A token placed in an
issue body with `match_body` on a code host, or in a conversation with
`match_body` on a model API, would do the same. The README's claim
that "a compromised workload can exfiltrate a token that's worthless
outside the proxy" holds for `inject`, not for `replace`.

Wraith Box closes this by design. The real credential goes only in
the header its binding injects, and a placeholder anywhere else gets a
403 with `placeholder-outside-binding`, or `model-placeholder-in-body`
in a conversation (S07-09-placeholder-binding). It also removes the
guest's own `Authorization`, `Proxy-Authorization` and `Cookie` on
every inspected host, and `Set-Cookie` from the responses of a bound
host (S07-08-requests-and-credentials). iron-proxy forwards the
workload's own credentials. Its `require` flag only refuses a request
that lacks the proxy token, and a `header_allowlist` drops only the
headers it doesn't name. It doesn't touch response cookies.

iron-proxy goes further than Wraith Box's specs on credential kinds.

- `aws_auth` takes a request the workload signed with placeholder AWS
  keys, removes that signature, and signs again with the real keys.
  `allowed_regions` and `allowed_services` limit what it signs.
- `gcp_auth` and `oauth_token` answer the token endpoint, and for
  Google the metadata server, themselves. So an SDK in the workload
  completes its usual token exchange with a placeholder, and the proxy
  injects the real token on the API requests.

S09-05-credentials plans SigV4 signing and OAuth 2 bindings, but says
neither how a guest SDK gets through its token exchange nor how a
SigV4 binding is scoped.

## Threats iron-proxy covers that Wraith Box doesn't

- **MCP tool calls through an allowed host.** An MCP server over
  Streamable HTTP takes every tool call as a `POST` to one path. Wraith
  Box's HTTP rules match method and path (S07-11-http-policy), so a
  rule that allows the endpoint allows every tool. iron-proxy parses
  the JSON-RPC body, refuses tools that aren't listed or whose
  arguments fail a rule, and removes refused tools from `tools/list`,
  per event on a server-sent event stream (`internal/mcp`). Wraith Box
  V1 doesn't copy an MCP server into the guest
  (S14-03-configuration-filter), refuses OpenShell's `mcp` endpoint key at load (S09-01-policy-files),
  and an approval grants read methods only
  (S07-17-approvals-and-learning). So in V1 this needs a user who
  allows `POST` to an MCP host in a policy file. It matters once MCP
  servers get credential bindings.
- **Writes judged by what they do.** A `judge` instance asks a small
  model whether a write, such as a `POST` to `api.github.com`, fits a
  policy written in prose. The judge can only refuse. A static deny
  applies before it, and when the model call fails the request is
  refused by default. This is iron-proxy's answer to the gap
  R01-defenseclaw found under SEC06-repo-writes: any write to a
  granted repository passes.
- **Unexpected request headers.** `header_allowlist` drops every
  request header a host's list doesn't name. T10-unnamed-credentials
  accepts that a guest's own credential reaches a host in a header
  that no profile names. A header list on a host with a binding would
  narrow that.

iron-proxy's other defenses are already in Wraith Box, in a stricter
form:

- Its default deny list has `169.254.169.254/32`, the AWS and Google
  Cloud IPv6 metadata addresses, and loopback. RFC 1918 is left out on
  purpose, for corporate networks. That list misses the ECS task
  credentials address `169.254.170.2`, Alibaba Cloud's metadata at
  `100.100.100.200`, NAT64 and 6to4. Wraith Box's list covers each
  (S07-07-hosts-and-modes, "Upstream address"). Both miss the Azure
  WireServer, `168.63.129.16`, which R01-defenseclaw already lists.
- It refuses a TLS request whose `Host` differs from its SNI, and
  paths with `.` or `..` segments. Wraith Box does both, and
  canonicalizes the path (S07-08-requests-and-credentials).
- It relays a WebSocket's bytes unread after the upgrade. Wraith Box's
  HTTP policy matches WebSocket messages (S07-11-http-policy).
- Its `sni-only` mode relays every host, with no check of ECH or a
  second ClientHello. Wraith Box allows `pass` per named host and
  parses the handshake (S07-16-tls-and-tunnels).

The PostgreSQL proxy has no counterpart. A database the guest reaches
by a raw TCP entry (S07-15-non-http-streams) needs a password in the
guest, against SEC04-no-guest-secrets. Wraith Box lacks that feature,
and it isn't on any version's list.

## Detection and response

R01-defenseclaw placed a detection layer in `wb-proxyd`, where the
guest can't turn it off. iron-proxy shows what such a layer looks like
in a shipping proxy:

- the `judge` transform, which only refuses, fails closed by default,
  and, when placed before `secrets` as its README recommends, sends
  the model provider placeholders rather than credentials;
- MCP decisions, with the tool and the reason, in each audit record;
- `body_capture`, which writes request bodies, such as prompts, to the
  audit log;
- OTLP export to a log store.

Its Respond is a config reload. It has no way to stop one workload or
revoke one grant.

A judge in `wb-proxyd` would send request content from the host to a
model provider. That is new egress from the host, it costs money per
request, and it sends data that S09-11-audit never records. Bodies in
the audit log would break that rule too. The open question of
R01-defenseclaw stands: which layer detects what, and what a finding
makes Wraith Box or the user do.

## Recommendations

- **Don't use iron-proxy as `wb-proxyd`, or as a module.** Its MPL-2.0
  dependencies rule the module out (NFR08-licensing), and its
  listeners, policy model and credential model differ from
  S07-egress-gateway. What fits is Go's standard library, which Wraith Box already uses.
- **Don't integrate.** Both want to be the egress. Chaining them would
  add a second inspecting proxy and a second credential holder, for
  nothing `wb-proxyd` lacks.
- **Use it as a reference while V1-M3-network-floor builds
  `wb-proxyd`.** Its fix history names problems the same code meets:
  hop-by-hop headers named in `Connection`, WebSocket relays that
  hang, leaf certificates that expire in the cache, case in host
  globs, percent-encoded reserved characters in the upstream path, and
  the CA missing from the served chain. Each belongs in a test.
- **Re-review after V1.** iron-proxy went from v0.1.0 to v0.54.0 in
  under seven months. A re-review after V1, as I240 does for
  R01-defenseclaw, updates this note and then files precise issues.
  This re-review and I240 can be one pass.

Candidates for that re-review, roughly in order of value:

- MCP-aware rules for when MCP servers get credential bindings: a tool
  allowlist with argument rules, and `tools/list` filtering. Decide
  between OpenShell's `mcp` endpoint key and a Wraith Box extension.
- For the SigV4 and OAuth 2 bindings of S09-05-credentials: scope a
  SigV4 binding by service and region, and let `wb-proxyd` answer the
  token endpoint so a guest SDK works with a placeholder. Copy
  iron-proxy's tests with its license notice where they fit.
- A request header allowlist per host with a binding, to narrow
  T10-unnamed-credentials.
- A refusing judge on write methods of the git hosting profile, as a
  Detection Finding rather than a control. This belongs to the Detect
  and Respond spec that R01-defenseclaw asks for, and comes after it.
- The Azure WireServer address, already a candidate of I240.

Not to adopt: `replace` substitution in place, `passthrough` names
that skip the proxy, a global SNI relay, `warn` mode across the whole
allowlist, OAuth requests forwarded without a token when the token
request fails (iron-proxy's default), request bodies in the audit
log, a CA key in a file, and an LLM judge as a control.

**Status:** Read 2026-10-23, re-review after V1 (no issue yet)
