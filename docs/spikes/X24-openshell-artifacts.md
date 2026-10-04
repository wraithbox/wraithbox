# X24 - OpenShell artifacts

Brief: B35-openshell-artifacts

**Purpose:** The result of the spike for I35. Do the OpenShell parts
that S07-egress-gateway, S09-policy-credentials-audit, S10-tech-stack and
S13-guest-confinement build on exist in the assumed form, under a
permissive license, at a version that can be pinned?

## For review

- **Decides:** the pins for the policy schema, the prover binary, the
  middleware API, OCSF, and the Seatbelt profiles. It also decides how
  Wraith Box hands policy to OpenShell's tooling, and what a boundary
  check result has to say before a policy is accepted.
- **You are approving:** the changes to S09-policy-credentials-audit
  ("Network policy schema", "Program narrowing", "Boundary check",
  "Approvals", "Audit"), S07-egress-gateway ("Dependency gate", one line
  in "Approvals and learning"), S10-tech-stack (the OpenShell rows) and
  S13-guest-confinement ("Source"). Every part exists under Apache-2.0
  at OpenShell v0.1.2, with two gaps. A policy file with Wraith Box's
  extension keys isn't a valid OpenShell policy. The proposal risk
  check isn't in the prover binary.
- **Controls touched:** SEC09-host-policy is kept: the boundary check
  accepts only `within_boundary` with network coverage, and every other
  result refuses. SEC14-no-fake-approvals is kept: a request whose risk
  check fails can't be approved. SEC10-audit: the OCSF classes are now
  named by number. SEC05-default-deny and SEC07-dep-gate are unchanged.
- **Assumed:** that OpenShell keeps the prover's JSON result
  (`schema_version` 1) and the `openshell.middleware.v1` package
  compatible within a minor release, as RFC 0014 (in review upstream)
  says for `v1` packages. Not run: the proposal risk check, and the
  Seatbelt profiles inside a guest.
- **Open decisions:** each has a recommendation in
  B35-openshell-artifacts.
  1. Which rules go to the prover at `wb trust`. The prover answers
     `unsupported` for GraphQL rules, and the git hosting profile has
     them, so each project with a remote on a git host would be refused. Recommended:
     send the user's and the repository's rules, and leave out the
     built-in profiles, which ship with Wraith Box and get their own
     tests.
  2. Where the approval risk check comes from. Recommended: Go code in
     `wb-hostd` that follows OpenShell's four categories, because
     OpenShell's check is a Rust library API, not a program, and
     S07-egress-gateway already asks for checks OpenShell doesn't have.
- **Brief:** B35-openshell-artifacts

## Question

From X00-index: do the OpenShell parts that S07-egress-gateway,
S09-policy-credentials-audit and S10-tech-stack build on exist in the
assumed form, under a permissive license, at a version that can be
pinned? The `network_policies` schema version 1, still valid with
Wraith Box's extra top-level keys; a standalone `openshell-prover`
binary and its reading of `binaries: /**`; the proposal risk check for
one rule; `supervisor_middleware.proto` (RFC 0009) used inside a Go
process. And the OCSF 1.8 classes and the agent-safehouse Seatbelt
profiles.

## Answer

**Yes, with conditions.** Each part, pinned:

| Part | Exists as assumed | Pin | License |
|---|---|---|---|
| `network_policies` schema, version 1 | yes, but extra top-level keys make the file invalid for OpenShell | OpenShell v0.1.2, commit `6648bd0c` | Apache-2.0 |
| `openshell-prover` binary | yes, macOS arm64 and Linux; no Windows build | 0.1.2, SHA-256 per archive, build attestation | Apache-2.0, Z3 (MIT) linked in |
| `binaries: /**` in the prover | yes, read as every program | as above | |
| Proposal risk check | no: a Rust library API inside OpenShell, not in the binary | as above | Apache-2.0 |
| `supervisor_middleware.proto` in a Go process | yes | OpenShell v0.1.2 | Apache-2.0 |
| OCSF 1.8 classes | yes: 4001, 4002, 5019, 2004 | `ocsf-schema` 1.8.0, commit `6fa6499a` | Apache-2.0 |
| agent-safehouse profiles | yes | v0.12.0, commit `6bded066` | Apache-2.0 |

The conditions, now in the specs:

1. Wraith Box writes a new document with only `version` and
   `network_policies` before it calls OpenShell's tooling.
2. The pin is an OpenShell release, not "schema version 1", because
   OpenShell adds fields without changing the version. OpenShell keys
   that Wraith Box doesn't implement are refused at load.
3. The boundary check accepts only `within_boundary` with
   `network_l4` and `network_rest` in its coverage. `unsupported`,
   `inconclusive` and errors refuse.
4. The approval risk check is Wraith Box's own (open decision 2).
5. The gate calls the generated Go interface as a method, and the
   proto files keep OpenShell's lint exceptions.

## Measurements

Upstream was read at OpenShell tag `v0.1.2` (commit
`6648bd0c290efbc41ba131ee9831ee45cd431f94`, 2026-09-28), the newest
stable release on 2026-10-05. Its `main` was at `71c3cd95` on 2026-10-03. Between the
two, the schema crate gained an MCP protocol revision, the middleware
proto didn't change, and the prover crates changed only a Z3 build
feature name and their packaging documentation.

### Policy schema

`openshell-policy-schema` (`crates/openshell-policy-schema/src/lib.rs`)
declares every struct `deny_unknown_fields` and checks each level
against a fixed key list before decoding. The top level allows
`version`, `filesystem_policy`, `landlock`, `process`,
`network_policies` and `network_middlewares`. The parser is bounded:
4 MiB, depth 64, 100,000 nodes, one document, no merge keys, duplicate
keys refused. `version` must be 1. The schema documentation says the
same ("OpenShell rejects a policy that contains unknown fields").

A Wraith Box file with a `wraithbox:` top-level key, run through the
prover, which uses the same parser:

```text
openshell-prover: invalid candidate policy: invalid policy: unknown field 'wraithbox' in authored policy
exit=2
```

The same file without that key: `within_boundary`, exit 0.

The schema has more than S09-policy-credentials-audit lists: endpoint
fields for credential rewriting (`credential_binding`,
`request_body_credential_rewrite`, `websocket_credential_rewrite`),
`allowed_ips`, `deny_rules`, GraphQL persisted queries, JSON-RPC and
MCP. A `binaries` entry has only `path`. The SHA-256 pin is applied at
runtime, not written in policy.

### Prover binary

The v0.1.2 release has `openshell-prover-aarch64-apple-darwin.tar.gz`
and Linux musl archives for x86-64 and arm64, with
`openshell-prover-checksums-sha256.txt`. The release has no Windows archive.
For the macOS archive:

- SHA-256
  `77f624498e1110abd0da26e926a71c834b5e58f8baa5a33252d59d7ab4629a53`,
  the same in the checksum file and in GitHub's asset digest.
- `gh attestation verify` passes: build provenance from
  `release-tag.yml` at `refs/tags/v0.1.2`, commit `6648bd0c`.
- `codesign -dv`: ad hoc signature. `otool -L` lists only system
  libraries, so Z3 is linked in statically.
- `openshell-prover --version`: `openshell-prover 0.1.2`. Its only
  command is `check <candidate> --boundary <file>`, with
  `--output text|json` and `--timeout` (default 10 s). Exit codes: 0
  within, 1 exceeds, 2 error, 3 unsupported or inconclusive.

### Boundary check runs

Each case against `boundary.yaml` (GitHub reads, PR creation in any
repository, npm reads, all with `binaries: /**`) unless named. Output
in `prover-output.txt` on the spike branch.

| Case | Result | Exit |
|---|---|---|
| Wraith Box file with a `wraithbox:` key | error, unknown field | 2 |
| same, key removed | `within_boundary` | 0 |
| candidate inside the boundary | `within_boundary` | 0 |
| adds `pastebin.com:443` and a `DELETE` | `exceeds_boundary`, `host=pastebin.com:443 protocol=l4` | 1 |
| adds only the `DELETE` | `exceeds_boundary`, `method=DELETE path=/repos/example/project` | 1 |
| `/**` against a boundary that allows only `/usr/bin/curl` | `exceeds_boundary`, `binary=/a binary_identity_required=true` | 1 |
| names `/opt/homebrew/bin/node` against `/**` | `within_boundary` | 0 |
| rule with no `binaries` | `within_boundary` | 0 |
| REST endpoint in `audit` mode | `unsupported`, "uses REST without enforced inspection" | 3 |
| GraphQL rule | `unsupported`, "uses protocol 'graphql'; only L4 TCP and REST are modeled" | 3 |

The JSON form has `schema_version: 1`, `prover_version: "0.1.2"`,
`result`, `exit_code`, `coverage.domains` (`filesystem`, `network_l4`,
`network_rest`, `process`, `landlock`), the counterexample, and
`reason_code`.

**How the prover reads `binaries`.** `containment.rs` solves the
network part twice, with `binary_identity_required` false and then
true. Without identity, `binaries` matches every program. With it, a
symbolic program path is any path under `/**`, and a rule's entries are
globs over the program and its ancestor. So `/**` covers every program,
as the `binary=/a` counterexample shows. A rule with no `binaries`
matches no program with identity, and every program without it.

**What it doesn't model.** The prover documentation lists `unsupported`
for: GraphQL, WebSocket, MCP and JSON-RPC rules; a REST endpoint not in
`enforce` mode; a host and port that has both a REST endpoint and one
without rules; query matchers and `?` or bracket patterns in paths; an
exact host and a wildcard host on one port with no `allowed_ips`; an
endpoint without `host`, or with both `port` and `ports`, IPv6 hosts,
and non-ASCII values. It returns `inconclusive` past 1,024 rules or 4,096
endpoints.

### Program narrowing in OpenShell

The rule engine (`crates/openshell-supervisor-network/data/sandbox-policy.rego`)
reads `data.runtime.require_binary_identity`, default true. When it is
false, `binary_allowed` holds for every program, so `binaries` doesn't
narrow. RFC 0012 says each isolation backend must resolve binary
identity, with no exempt mode. S09-policy-credentials-audit said
OpenShell evaluates rules without binary identity "for its Windows
driver". The pinned source doesn't show that. The MXC driver RFC (0013)
hands `network_policies` to a host proxy that keeps per-binary scope.
The spec now names the switch instead.

### Proposal risk check

This part wasn't run. The standalone binary has only `check`. The risk check is
`openshell_prover::queries::run_all_queries` over a model built from a
policy, a credential set and a binary registry, and the step that keeps
only new findings (`finding_delta`) is in `openshell-server`, the
gateway. The prover's README says the crate isn't a stable published
SDK, and calls the risk queries "legacy" until a managed-policy
migration. A Rust driver that links the crate wasn't built, so this
part comes from reading the source.

The four categories, from `queries.rs`:

| Category | Fires when |
|---|---|
| `link_local_reach` | a rule reaches `169.254.0.0/16`, `fe80::/10` or `metadata.google.internal` |
| `l7_bypass_credentialed` | a program whose registry entry has a non-HTTP protocol reaches a host with a credential |
| `credential_reach_expansion` | a program reaches a host and port with a credential that it didn't reach before |
| `capability_expansion` | a new method on a program, host and port that already had credentialed reach |

Credentials come from a separate YAML file of names, scopes and target
hosts. Programs come from a registry of nine descriptors (`claude`,
`curl`, `gh`, `git`, `nc`, `node`, `python3`, `ssh`, `wget`). A path
not in the registry, `/**` included, gets the "unknown binary"
descriptor (`registry.rs`, `get_or_unknown`): it can exfiltrate and
build HTTP requests, and has no protocols, so it never bypasses L7. For
Wraith Box's `/**` rules, `l7_bypass_credentialed` therefore never
fires, even though `/**` covers `ssh` and `nc`.

Traced through the source by hand, not run, for one rule: adding
`POST /repos/example/project/pulls` on `api.github.com`, with a GitHub
credential bound and a baseline that only reads, would give one
`capability_expansion` path for `/**` with method `POST`, because the
reach existed and the method is new. Adding a first rule for
`api.github.com` would give `credential_reach_expansion`, and
`finding_delta` would drop the per-method paths. A rule for
`169.254.169.254` would give `link_local_reach`.

S07-egress-gateway and S09-policy-credentials-audit ask the risk check
for more than these four (X22-no-guest-credentials adds shared
object-storage hosts and signature parameters on a write method), and
for new write methods on hosts without a credential, which
`capability_expansion` doesn't cover.

### Middleware API

`proto/supervisor_middleware.proto` (720 lines, package
`openshell.middleware.v1`) imports `extension.proto` and two
well-known types. It sets no `go_package`. It defines
`SupervisorMiddleware` (`Describe`, `ValidateConfig`,
`EvaluateHttpRequest`, `EvaluateWebSocketSession`) and
`HttpResponsePreReturn` (`Evaluate`, a stream over response preflight,
body units and trailers). RFC 0009 is `accepted`. It describes built-in
middleware that runs in process and operator-run services reached over
gRPC. Its text calls response inspection future work, while the proto
and the operations documentation at v0.1.2 already have
`HTTP_RESPONSE/PRE_RETURN`.

`buf generate` (buf 1.72.0, the Go protobuf plugin 1.36.11, the gRPC plugin
1.6.2) with `M` import-path options produced
`supervisor_middleware.pb.go`, `supervisor_middleware_grpc.pb.go` and
`extension.pb.go`. `buf lint` with the `STANDARD` rules flags the
service and message names that OpenShell's own `buf.yaml` exempts. A
stub gate that implements `SupervisorMiddlewareServer` and refuses one
npm tarball, called as a Go method, then served on an in-memory gRPC
connection (`middleware-output.txt`):

```text
in-process /left-pad/-/left-pad-1.3.0.tgz -> DECISION_ALLOW  err=<nil>
in-process /left-pad/-/left-pad-9.9.9.tgz -> DECISION_DENY dependency_too_young err=<nil>
grpc       /left-pad/-/left-pad-9.9.9.tgz -> DECISION_DENY dependency_too_young err=<nil>
Describe (not implemented) err=rpc error: code = Unimplemented desc = method Describe not implemented
```

The interface fits use inside `wb-proxyd`. Two limits in OpenShell's
documentation are limits of OpenShell's proxy as the caller, not of the
API: it offers no body inspection of compressed responses, or of
responses with `Cache-Control: no-transform`, and it doesn't relay a
middleware's free-form `reason` to the client (it may relay
`reason_code`). When `wb-proxyd` is the caller, neither applies.

RFC 0014 (state `review`) makes a `v1` protobuf package Stable:
backward compatible across patch releases, breaking only in a minor
release with notice.

### OCSF

OpenShell's `openshell-ocsf` crate sets `OCSF_VERSION` to `1.8.0` and
emits Network Activity (4001), HTTP Activity (4002), Device Config
State Change (5019) and Detection Finding (2004), among others. In
`ocsf-schema` tag `1.8.0` (commit `6fa6499a`, Apache-2.0):
`events/network/network_activity.json` (uid 1),
`events/network/http_activity.json` (uid 2),
`events/discovery/device_config_state_change.json` (uid 19) and
`events/findings/detection_finding.json` (uid 4), in categories 4, 4,
5 and 2. OCSF 1.9.0 was released on 2026-08-03, and OpenShell v0.1.2
still emits 1.8.0.

### agent-safehouse

`github.com/eugene1g/agent-safehouse`, tag `v0.12.0` (commit
`6bded066`, 2026-09-07), Apache-2.0, no `NOTICE` file. `profiles/` has
70 Seatbelt profile files in layers: base (`(deny default)`, with
`HOME_DIR` and `WORK_DIR` placeholders), system runtime, network,
toolchains, integrations (Xcode among them), agents (`claude-code.sb`
among them) and apps. `bin/safehouse.sh` joins the selected modules and
runs the result with `sandbox-exec`. The network module allows every
outbound connection, and its comments say that stopping data from leaving
isn't its goal. Writes go to temporary directories and the work
directory.

## What it means for the specs

- S09-policy-credentials-audit: the pin, closed keys, refusing keys
  Wraith Box doesn't implement, the document handed to OpenShell's
  tooling, the `require_binary_identity` correction, the prover's
  reading of `/**`, the boundary check's acceptance rule and its
  rules it can't model, the risk check's source, and the OCSF class numbers.
- S07-egress-gateway: the gate calls the generated interface in
  process, and isn't bound by OpenShell's caller limits. The approval
  line says "the risk check", not "the prover's".
- S10-tech-stack: pins, checksums, attestation, the missing Windows
  build, a row for the risk check, the proto files' lint exceptions.
- S13-guest-confinement: the safehouse pin and how its profiles are
  reused.

## Not covered

- The risk check wasn't run (see above). An implementation should
  test against upstream's own cases in `queries.rs`.
- The safehouse profiles weren't run in a guest. The Layer 2 work of
  S13-guest-confinement has to.
- The Linux prover archives weren't downloaded or run.
- A boundary policy for Wraith Box's default profiles wasn't written.

Spike code:
[`fde7a3c`](https://github.com/wraithbox/wraithbox/tree/fde7a3cf2d968f309611575eb53c22511dfebc1f/spikes/x24-openshell-artifacts).

**Status:** Answered 2026-10-05: yes, with conditions
