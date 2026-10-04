# 009 - Policy, Credentials, Certificates and Audit

**Purpose:** Where policy comes from, how secrets are held, how TLS
inspection is trusted, and what is recorded.

**Requirements:** F9-approve-unknown, F10-learn-mode, F15-inspect, S4-no-guest-secrets to S6-repo-writes, S9-host-policy, S10-audit, S12-least-privilege, S14-no-fake-approvals, N6-explained-refusals.

## Policy

- **Files.** Two kinds, both on the host, outside every repository and
  every guest, and the same on every host OS (F17-same-everywhere). `<config>` is per OS
  in spec 012 (`~/.config/wraithbox` on macOS).
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
  separate top-level keys. The OpenShell part is then a valid OpenShell
  policy by itself.
- **Program narrowing.** `binaries` entries match the program label
  from the guest (spec 013). Until spike X14 has delivered labels,
  rules are evaluated the way OpenShell does when binary identity is
  not required (as for its Windows driver): `binaries` does not narrow
  a rule. Each rule lists the universal entry `path: "/**"`, and policy
  that names programs is rejected at load with an error
  saying why (N6-explained-refusals), so no rule is silently weaker than written. Whether
  the prover reads `/**` as every program is checked when the boundary
  check is built.
- **Settings contents.** The project toolchain manifest (Brewfile on
  macOS guests); extra writable repositories; guest OS; VM slot
  (work/isolated); resource limits; clipboard and port-forwarding
  switches.
- **Repository-supplied configuration.** A `.wraithbox/` directory in a
  repository (`config.toml`, `policy.yaml`) is ignored unless the user
  has run `wb trust` for that repository. Even then it may only add
  allowed hosts, a toolchain manifest and HTTP rules, and every addition
  is shown in `wb policy explain`. It can never add credential bindings
  or switch hosts to pass-through (S9-host-policy).
- **Boundary check.** At `wb trust` and whenever a trusted repository's
  policy changes, the merged project policy is checked against a
  boundary policy (the most a project may ever be allowed) with
  OpenShell's prover (`openshell-prover`, run as an external program).
  A policy that exceeds the boundary is refused, and the prover's
  counterexample is shown. The default boundary ships with Wraith Box,
  and an organization may supply its own.
- **Precedence.** Built-in defaults → global → project → trusted repo
  config → session approvals. `wb policy explain` shows the effective
  value and its source.

## Credentials

- **Storage.** Each credential is an item in the platform's secret
  store (spec 012), readable only by `wb-proxyd`. On macOS: a Keychain
  item in an access group bound to Wraith Box's code-signing identity
  once that identity exists; until then, an access-control list naming
  the `wb-proxyd` binary, which is weaker and documented as such. Other
  platforms scope items as narrowly as their store allows; the limits
  are documented per platform in spec 012.
- **Bindings.** A binding names the hosts, ports, and paths it applies
  to, the header and scheme to inject, and the secret store item
  (spec 007 for placeholders found elsewhere). Besides static values,
  a binding can hold an OAuth 2 refresh token or client credentials,
  which `wb-proxyd` exchanges for short-lived access tokens, or AWS keys
  for SigV4 signing. `wb cred set <binding>` reads the
  value from a prompt or stdin; it never appears in arguments or logs.
- **Model credential.** Claude Code in the guest is configured with a
  placeholder and a binding for the model API host. Whether every Claude
  Code authentication mode works with host-side replacement (including
  token refresh) is the first spike in spec 011. OpenShell has shown
  that an API key works this way. A Claude subscription login is the
  open part (spike X1).
- **Never in the guest.** Images are scanned for secrets at seal time;
  the environment of every guest process is built from an allowlist.

## TLS inspection certificate authority

- **Key.** A P-256 key generated in the platform's hardware key store
  (Secure Enclave on macOS, TPM elsewhere; spec 012), non-exportable,
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

## Approvals (S14-no-fake-approvals)

Before an approval request is shown, the rule it would add goes
through the prover's proposal risk check (new reach for a credential,
new write methods, metadata addresses). Findings are part of the
request. Only the user approves a request.

Approval requests are delivered as native notifications (through the
platform's notification helper, spec 012) and through `wb approve` /
`wb deny`. They are never written to the terminal stream of an agent
session, which the guest controls. Guest-influenced text in a request is
stripped of control and escape sequences wherever it is shown.

## Audit (S10-audit)

- JSONL in `<logs>` (spec 012; `~/Library/Logs/WraithBox/` on macOS),
  written by `wb-hostd` from events
  sent by `wb-netd` and `wb-proxyd`; rotated and size-capped.
- Events use OCSF 1.8 classes, as OpenShell's do: network activity for
  connections, HTTP activity for inspected requests, configuration
  state change for policy and approvals, and detection finding for
  refused placeholders, pin mismatches, and other signs of an attack. A
  SIEM can read the log without a custom parser.
- Records: timestamp; project; session; guest user; destination host and
  port; decision and the rule that made it; HTTP method and path for
  inspected requests; bytes in and out; approval actions; returned work
  and its flags. Process attribution reported by the guest is stored as
  an untrusted label.
- Never recorded: credential values and request or response bodies.

**Status:** Draft
