# S09 - Policy, Credentials, Certificates and Audit

**Purpose:** Where policy comes from, how secrets are held, how TLS
inspection is trusted, and what is recorded.

**Requirements:** FR09-approve-unknown, FR10-learn-mode, FR15-inspect, SEC04-no-guest-secrets to SEC06-repo-writes, SEC09-host-policy, SEC10-audit, SEC12-least-privilege, SEC14-no-fake-approvals, NFR06-explained-refusals.

## Policy

- **Files.** Two kinds, both on the host, outside every repository and
  every guest, and the same on every host OS (FR17-same-everywhere). `<config>` is per OS
  in S12-platforms (`~/.config/wraithbox` on macOS).
  - *Settings* in TOML: `<config>/config.toml` and
    `<config>/projects/<project-id>.toml`.
  - *Network policy* in YAML: `<config>/policy.yaml` and
    `<config>/projects/<project-id>.policy.yaml`.
- **Network policy schema.** The `network_policies` part of OpenShell's
  policy schema (version 1): named rules of endpoints (host, port, path,
  protocol, access preset or method and path rules, `enforce` or
  `audit`) and `binaries`. Wraith Box parses and validates it in Go with
  its own code. Reusing the schema lets OpenShell's prover check Wraith
  Box policy, and gives users one policy language for both tools.
  Wraith Box extensions (per-host inspect or pass mode, wildcard
  budget, dependency-gate thresholds, credential bindings) go in
  separate top-level keys.
  - *Pinned release.* OpenShell adds fields to the schema without
    changing `version: 1`, so the pin is an OpenShell release: v0.1.2
    (X24-openshell-artifacts, S10-tech-stack). Wraith Box accepts the
    keys of that release only.
  - *Closed keys.* OpenShell rejects a policy with an unknown key at
    any level, so a file with Wraith Box's extension keys isn't a valid
    OpenShell policy (X24-openshell-artifacts). Wraith Box keeps the
    extensions in the same file. When it hands policy to OpenShell's
    tooling, it writes a new document with only `version` and
    `network_policies`, never the user's file.
  - *Unimplemented keys.* An OpenShell key that Wraith Box doesn't
    implement is refused at load with an error that names it
    (NFR06-explained-refusals), never ignored, so no rule is weaker
    than written. Examples are the other top-level keys
    (`filesystem_policy`, `network_middlewares`) and endpoint fields
    such as `credential_binding` and `mcp`.
- **Program narrowing.** `binaries` entries match the program label
  from the guest (S13-guest-confinement). Until spike X14-flow-attribution has delivered labels,
  `binaries` does not narrow a rule. OpenShell's policy engine has the
  same mode, switched by its `require_binary_identity` setting, though
  its isolation backends must resolve binary identity
  (X24-openshell-artifacts). Each rule lists the universal entry
  `path: "/**"`, and policy that names programs is rejected at load
  with an error saying why (NFR06-explained-refusals), so no rule is
  silently weaker than written. The prover checks each policy both with
  and without binary identity, and with it reads `/**` as every program
  (X24-openshell-artifacts).
- **Settings contents.** The project toolchain manifest (Brewfile on
  macOS guests); extra writable repositories; guest OS; VM slot
  (work/isolated); resource limits. Clipboard and port-forwarding
  switches come after V1 (V1-08-no-forward-clipboard).
- **Repository-supplied configuration.** A `.wraithbox/` directory in a
  repository (`config.toml`, `policy.yaml`) is ignored unless the user
  has run `wb trust` for that repository. Even then it may only add
  allowed hosts, a toolchain manifest and HTTP rules, and every addition
  is shown in `wb policy explain`. It can never add credential bindings
  or switch hosts to pass-through (SEC09-host-policy).
- **Boundary check.** At `wb trust` and whenever a trusted repository's
  policy changes, the merged project policy is checked against a
  boundary policy (the most a project may ever be allowed) with
  OpenShell's prover (`openshell-prover check --output json`, run as an
  external program, S10-tech-stack). A policy is accepted only when the
  result is `within_boundary` and `coverage.domains` lists
  `network_l4` and `network_rest`. A policy that exceeds the boundary is
  refused, and the prover's counterexample is shown. Every other result
  (`unsupported`, `inconclusive`, `error`, a missing or unknown field)
  refuses the policy too, with the prover's `reason_code` and reason
  shown and logged. The default boundary ships with Wraith Box,
  and an organization may supply its own.
  - *What the prover models.* At v0.1.2 it compares hosts, ports and
    programs, and method and path rules on `protocol: rest` endpoints
    in `enforce` mode. It answers `unsupported` for GraphQL and
    WebSocket rules, an endpoint in `audit` mode, query matchers, and a
    host and port that has both a REST endpoint and one without rules
    (X24-openshell-artifacts). The git hosting profile has GraphQL
    rules (S07-egress-gateway), so a merged policy that includes it is
    refused. Which rules go to the prover, so that `wb trust` can pass
    for such a project, is open (B35-openshell-artifacts). Until it is
    decided, the whole merged policy goes to the prover and these
    results refuse it.
- **Precedence.** Built-in defaults → global → project → trusted repo
  config → session approvals. `wb policy explain` shows the effective
  value and its source.

## Credentials

- **Storage.** Each credential is an item in the platform's secret
  store (S12-platforms), readable only by `wb-proxyd`. On macOS: a Keychain
  item in an access group bound to Wraith Box's code-signing identity
  once that identity exists; until then, an access-control list naming
  the `wb-proxyd` binary, which is weaker and documented as such. Other
  platforms scope items as narrowly as their store allows; the limits
  are documented per platform in S12-platforms.
- **Bindings.** A binding names the hosts, ports, and paths it applies
  to, the header and scheme to inject, and the secret store item
  (S07-egress-gateway for placeholders found elsewhere). Besides static values,
  a binding can hold an OAuth 2 refresh token or client credentials,
  which `wb-proxyd` exchanges for short-lived access tokens, or AWS keys
  for SigV4 signing. `wb cred set <binding>` reads the
  value from a prompt or stdin; it never appears in arguments or logs.
  A binding can also hold a fixed public value in place of a secret
  store item, for a host that answers public reads only to a request
  with a token. Built-in profiles ship these, such as the anonymous
  `ghcr.io` token for Homebrew bottles (S07-egress-gateway,
  X22-no-guest-credentials). Such a value isn't a secret, and the
  guest doesn't get a placeholder for it.
- **Model credential.** Claude Code in the guest is configured with a
  placeholder and a binding for the model API host. Whether every Claude
  Code authentication mode works with host-side replacement (including
  token refresh) is the first spike in X00-index. OpenShell has shown
  that an API key works this way. A Claude subscription login is the
  open part (spike X01-model-credential).
- **Never in the guest.** Images are scanned for secrets at seal time;
  the environment of every guest process is built from an allowlist.

## TLS inspection certificate authority

- **Key.** A P-256 key generated in the platform's hardware key store
  (Secure Enclave on macOS, TPM elsewhere; S12-platforms), non-exportable,
  used by `wb-proxyd` to sign leaf certificates (Go's certificate
  creation accepts any signer). Fallback if no hardware key store is
  available: a software key held in the secret store.
- **Scope.** The CA certificate has name constraints restricting it
  to the hostnames configured for inspection. It is trusted **only inside
  guests**, never on the host. When the inspected set changes, a new CA
  is issued and `wb-guestd` installs it.
- **Lifetimes.** CA: 30 days, rotated automatically. Leaf certificates:
  24 hours, cached in memory.
- **Guest trust.** `wb-guestd` installs the CA in the guest OS's system
  trust store and sets toolchain-specific trust variables so that every
  common client accepts it.

## Approvals (SEC14-no-fake-approvals)

Before an approval request is shown, the rule it would add goes
through a risk check (new reach for a credential,
new write methods, metadata addresses, shared object-storage hosts and
wildcards over them, signature parameters on a write method,
S07-egress-gateway). Findings are part of the request. Only the user approves a request.

The risk check compares the policy with and without the proposed rule
and reports what the rule adds. OpenShell's proposal risk check is the
model for its first four checks, with the same category names:
`link_local_reach`, `credential_reach_expansion`,
`capability_expansion` and `l7_bypass_credentialed`. OpenShell's check
can't be run as it is. It is a Rust library API that OpenShell's gateway
calls, isn't in the standalone `openshell-prover` binary, and has none
of the checks after the first four (X24-openshell-artifacts). It also
reads `/**` as a single unknown program that speaks HTTP, so it never
reports `l7_bypass_credentialed` for a `/**` rule. Wraith Box's check
treats `/**` as covering programs that don't speak HTTP too. Where the
check runs and in which language is open (B35-openshell-artifacts). A
request whose risk check fails or can't run can't be approved: the
destination stays denied, and the failure is logged.

Approval requests are delivered as native notifications (through the
platform's notification helper, S12-platforms) and through `wb approve` /
`wb deny`. They are never written to the terminal stream of an agent
session, which the guest controls. Guest-influenced text in a request is
stripped of control and escape sequences wherever it is shown.

## Audit (SEC10-audit)

- JSONL in `<logs>` (S12-platforms; `~/Library/Logs/WraithBox/` on macOS),
  written by `wb-hostd` from events
  sent by `wb-netd` and `wb-proxyd`; rotated and size-capped.
- Events use OCSF 1.8.0 classes, as OpenShell's do
  (X24-openshell-artifacts): Network Activity (4001) for
  connections, HTTP Activity (4002) for inspected requests, Device
  Config State Change (5019) for policy and approvals, and Detection
  Finding (2004) for
  refused placeholders, foreign credentials removed from a request
  (S07-egress-gateway), pin mismatches, and other signs of an attack. A
  SIEM can read the log without a custom parser.
- Records: timestamp; project; session; guest user; destination host and
  port; decision and the rule that made it; HTTP method and path for
  inspected requests; bytes in and out; approval actions; returned work
  and its flags. Process attribution reported by the guest is stored as
  an untrusted label.
- Never recorded: credential values and request or response bodies.

**Status:** Draft
