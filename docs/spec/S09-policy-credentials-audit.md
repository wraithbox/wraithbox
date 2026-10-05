# S09 - Policy, Credentials, Certificates and Audit

**Purpose:** Where policy comes from, how secrets are held, how TLS
inspection is trusted, and what is recorded.

**Requirements:** FR09-approve-unknown, FR10-learn-mode, FR15-inspect, SEC04-no-guest-secrets to SEC06-repo-writes, SEC08-proj-isolation to SEC10-audit, SEC12-least-privilege, SEC14-no-fake-approvals, NFR06-explained-refusals. Residual risk T11-shared-vm-grants.

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
  - *Implemented keys.* Per endpoint, Wraith Box implements `host`,
    `port` or `ports`, `path`, `protocol`, `enforcement`, `access`,
    `rules` and `deny_rules`. The built-in profiles need `deny_rules`
    for denies such as the git hosting profile's archive deny. The
    prover's handling of `deny_rules` wasn't tested
    (X24-openshell-artifacts), and is tested before the boundary check
    relies on it.
  - *Refused keys.* Any other OpenShell key is refused at load with an
    error that names it (NFR06-explained-refusals), never ignored, so
    no rule is weaker than written. Examples are the other top-level
    keys (`filesystem_policy`, `network_middlewares`) and the endpoint
    fields `credential_binding`, `mcp` and `allowed_ips`. `allowed_ips`
    would allow destinations by raw IP address (SEC05-default-deny).
  - *Strings.* Every rule name, host, path and method must be ASCII,
    with no control bytes, and within a length cap fixed in code, or
    the file is refused at load. The prover models ASCII values only,
    and its output repeats these strings (X24-openshell-artifacts).
  - *What the prover sees.* The document Wraith Box writes for the
    prover allows at least what `wb-proxyd` enforces, never less. A
    host in pass mode is written as an L4 endpoint with no `protocol`
    and no rules, whatever rules the file gives it, so the boundary
    must allow L4 to that host. The prover can't see the extension
    keys, so Wraith Box's own Go check compares them with the
    boundary's extension part: each pass host must be a pass host in
    the boundary, dependency-gate thresholds can't be looser, and the
    wildcard budget can't be larger. Any mismatch refuses the policy.
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
- **Project narrowing.** A project's rules and bindings apply to the
  whole VM while the project has a session in it (S07-egress-gateway,
  "Enforced per VM"). Once X14-flow-attribution delivers labels that
  name the project user (S13-guest-confinement), those labels may
  narrow a project's rules to its own flows. The rules of program
  narrowing apply: a missing or malformed label matches no narrowed
  rule. Until then nothing is narrowed by project.
- **Settings contents.** The project toolchain manifest (Brewfile on
  macOS guests); extra writable repositories; guest OS; VM slot
  (work/isolated); resource limits. Clipboard and port-forwarding
  switches come after V1 (V1-08-no-forward-clipboard). A project whose
  credentials or write grants must stay apart from other projects is
  set to the isolated slot, and then gets the isolated VM to itself
  unless a `shared` switch says otherwise (S06-vm-lifecycle, "VMs",
  T11-shared-vm-grants).
- **Repository-supplied configuration.** A `.wraithbox/` directory in a
  repository (`config.toml`, `policy.yaml`) is ignored unless the user
  has run `wb trust` for that repository. Even then it may only add
  allowed hosts, a toolchain manifest and HTTP rules, and every addition
  is shown in `wb policy explain`. It can never add credential bindings
  or switch hosts to pass-through (SEC09-host-policy).
  - *Closed keys.* It is parsed against a closed list of those keys, and
    any other key is a load error.
  - *Which commit.* It is read only from the host repository's
    checked-out `HEAD`, never from the landing, quarantine or export
    repository (S08-workspace-and-git), so the guest can't change it by
    pushing. Returned work that changes `.wraithbox/` is flagged
    (S08-workspace-and-git). The guest can still propose such a change,
    so the parser has a fuzz target (S11-verification-and-spikes,
    network policy YAML).
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
  - *How the prover runs.* The prover parses repository bytes, so it
    doesn't run with `wb-hostd`'s access. `wb-hostd` writes the
    candidate and the boundary to two documents and starts `wb-prover`
    through `wb-launcher` (S04-architecture). The two documents and the
    pipe for the result are inherited descriptors, never paths in the
    request, so `wb-prover` adds no exception to the launcher's "no
    arguments" rule. The prover reads them as `/dev/fd/N`, or
    `wb-prover` copies them into its own confined temporary directory
    first. `wb-prover` sets its memory cap on itself before it runs
    the prover, because `wb-hostd` isn't its parent, then confines
    itself to those descriptors, with no network, and runs the prover
    with fixed arguments. `wb-hostd` kills it past a wall-clock limit.
    A kill or any non-zero exit refuses the policy
    (SEC12-least-privilege, SEC13-bounded-resources).
  - *Which binary.* The prover comes from the installed bundle,
    re-signed with Wraith Box's signing identity at release. Before it
    runs the prover, `wb-prover` checks the binary's SHA-256 against the
    value pinned at release. A mismatch refuses the policy and is a
    Detection Finding (2004) event. The release checks OpenShell's
    build attestation, not the run (S10-tech-stack).
  - *Output.* `wb trust` shows the prover's reason and counterexample
    stripped of control and escape sequences, as approval text is.
  - *User policy.* The user's global and project network rules are
    checked against the boundary too, at session start and whenever
    they change, also for a project with no trusted repository. The
    candidate is the global rules and the rules of every project with
    a session in the VM, which is what the VM enforces
    (S07-egress-gateway, "Enforced per VM"), projected as above,
    without the built-in profiles. A failing check refuses the session
    start, with the reason, the project and the rule that caused it
    shown and logged. While sessions run, a change that only narrows
    the union applies without this check. A change that widens it and
    fails the check, or can't be checked, is refused, the VM keeps its
    last policy that passed, and `wb policy explain` shows the change
    as pending (S07-egress-gateway, "Policy changes while sessions
    run"). The result is cached on the SHA-256
    of the candidate and of the boundary, so an unchanged policy adds
    no prover run to session start (NFR01-startup). This is option A of
    the open decision on approvals and the boundary
    (B35-openshell-artifacts), which the spec applies until it is
    decided.
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
  - *Network rules merge as a union.* Rules are keyed by source and
    rule name, so a later source adds rules and never replaces one. A
    rule in user or repository policy with the name of a built-in
    profile's rule is a load error. Built-in denies apply whatever the
    source of the allow. User and repository policy can't change a
    built-in host's kind, mode or rules.
  - *Per VM.* The host enforces the merge for a VM, not for a project:
    the project sources are those of every project with a session in
    the VM. Limits take the strictest value among them, and so does
    `enforce` over `audit`. A conflict between projects, such as a host
    in pass mode for one and inspected or bound for another, or two
    bindings for one host and path, refuses the joining session (S07-egress-gateway, "Enforced per
    VM"). The extension check above runs over the union at each
    recompute. `wb policy explain` shows for each rule and each
    credential binding the project it comes from and the projects
    whose sessions can reach it now, which are all the projects with a
    session in the same VM (T11-shared-vm-grants).

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
- **Reach.** A project's binding is injected for requests from the whole
  VM while the project has a session in it, so any process in that VM
  can have it used on its behalf, though none can read it
  (T11-shared-vm-grants). `wb policy explain` shows who can reach it
  ("Precedence").
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
through a risk check, which compares the policy with and without the
rule and reports what the rule adds. Findings are part of the request.
Only the user approves a request.

- **Approved rule.** An approval adds one rule of a fixed form: one
  exact host with no wildcard, `protocol: rest`, `enforcement: enforce`,
  `access: read-only` (GET, HEAD and OPTIONS) and `binaries: /**`. A
  write method needs a separate request (S07-egress-gateway), which
  adds a rule of the same fixed form with one named method and the
  exact path, or path prefix, the request used in place of
  `access: read-only`. Both forms only allow: no denies, no `audit`
  mode, no extension keys.
- **Host name.** The host in an approved rule comes from the guest. It
  must pass the same ASCII, control-byte and length rules as policy
  strings before the risk check or the prover sees it, or the request
  stays denied and is logged with the rule.
- **Checks.** Each check reports under a name. The first four take
  their names from OpenShell's proposal risk check:
  - `link_local_reach` (OpenShell): the rule reaches a link-local or
    cloud metadata address.
  - `credential_reach_expansion` (OpenShell): the rule gives a host and
    port with a credential binding reach it didn't have.
  - `capability_expansion` (OpenShell): the rule adds a method on a
    host that already had reach with a credential.
  - `l7_bypass_credentialed` (OpenShell's name, Wraith Box's meaning):
    the rule puts a host with a credential binding into pass mode, or
    gives it an L4-only endpoint. `wb-proxyd` injects credentials only
    into inspected HTTP, so no program that doesn't speak HTTP gets a
    credential, and this is the bypass that matters. OpenShell keys
    this check on its list of known programs, and it never fires for a
    `/**` rule (X24-openshell-artifacts).
  - Wraith Box only: a first write method on a host without a
    credential (S07-egress-gateway, "What an approval grants").
  - Wraith Box only: a shared object-storage host or a wildcard over
    one, and signature parameters on a write method (S07-egress-gateway,
    X22-no-guest-credentials).
  - Wraith Box only: a host that mirrors a gated registry
    (S07-egress-gateway, "Dependency gate", the condition on which
    T07-ungated-sources was accepted).
- **Source.** OpenShell's check can't be run as it is. It is a Rust
  library API that OpenShell's gateway calls, and isn't in the
  standalone `openshell-prover` binary (X24-openshell-artifacts). Where
  Wraith Box's check runs and in which language is open
  (B35-openshell-artifacts).
- **Boundary.** A request whose rule would leave the boundary isn't
  shown. The prover checks the rule alone, in the approved form,
  against the boundary. An approved rule only allows, so a rule inside
  the boundary can't take a policy that is inside it outside. The
  user's own policy is checked at session start for that reason
  ("Boundary check" above). A refusal is logged as a Device Config
  State Change (5019) event.
  Whether approvals are bound by the boundary at all is open
  (B35-openshell-artifacts), and until it is decided they are.
- **Limits.** The guest triggers approval requests, one per unknown
  name it looks up (S07-egress-gateway), so prover runs are bounded
  (SEC13-bounded-resources). Requests are deduplicated by host and
  rate-limited per VM, with a limit fixed in code, because the host
  can't tell which session raised one (S07-egress-gateway, "Enforced
  per VM"). Each VM has a
  limit fixed in code on pending requests, and a cap on concurrent
  prover runs (S04-architecture). Boundary results are cached per host,
  port and rule form, keyed on the SHA-256 of the boundary document, so
  a changed boundary is checked again. A request over a limit stays
  denied and is logged with the rule that limited it.
- **Failure.** A request whose risk check or boundary check fails or
  can't run can't be approved: the destination stays denied, and the
  failure is logged.
- **Join approval.** When the risk check finds something in a joining
  project's rules and bindings compared with the VM's current union
  (S07-egress-gateway, "Enforced per VM"), the session start waits on
  a join approval. It is delivered like any approval request and lists
  the joining project, the projects already in the VM and each
  finding. The answer is a Device Config State Change (5019) event
  that records the projects, the findings, the decision, and the
  SHA-256 of the joining project's policy and of the union. An
  approval is cached on that pair, so the next start with the same
  policy and the same union doesn't ask again, and any change to
  either asks again. A denial, or no answer before the request
  expires, refuses the start.

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
- Records: timestamp; VM; project, session and guest user (for
  network events, derived from the guest's label, see below);
  destination host and port; decision and the rule that made it; HTTP
  method and path for inspected requests; bytes in and out; approval
  actions; returned work and its flags. Process attribution reported by
  the guest is stored as an untrusted label.
- Attribution of network events. `wb-netd` and `wb-proxyd` know the VM,
  and `wb-hostd` adds the projects and sessions running in it. The
  project, session, guest user and program of each connection come
  only from the guest's label, stored as untrusted (S07-egress-gateway,
  "Enforced per VM"). The per-session audit log of FR15-inspect holds
  returned work and approvals by the host's attribution, and network
  events by the guest's label, shown as reported by the guest.
  Returned work keeps the project and session of its git transport
  (S08-workspace-and-git).
- Never recorded: credential values and request or response bodies.

**Status:** Draft
