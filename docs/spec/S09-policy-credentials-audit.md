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
  - *Pass hosts.* Only the global and project network policy files can
    put a host in pass mode, never repository configuration. Each pass
    entry names one exact host and needs a reason, which follows the
    string rules above. A wildcard entry, or an entry for a host with a
    credential binding or one a built-in profile covers, is refused at
    load, and adding a pass host is a Device Config State Change (5019)
    event. The maintainer decided this
    on I40 (B40-learn-pass-modes), and S07-egress-gateway, "Modes, per
    host", has the rules.
  - *What the prover sees.* The document Wraith Box writes for the
    prover allows at least what `wb-proxyd` enforces, never less. A
    built-in GraphQL rule is written as a REST rule that allows `POST`
    to its GraphQL path on its host, which is wider than the rule
    ("Built-in profiles in the check"). A host in pass mode is written
    as an L4 endpoint with no `protocol` and no rules, whatever rules
    the file gives it, so the boundary must allow L4 to that host. The
    prover can't see the extension keys, so Wraith Box's own Go check
    compares them with the
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
    any other key is a load error. A rule on a built-in host whose
    profile part is off is a load error too (S07-egress-gateway,
    "Profile parts").
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
    a session in the VM, with the built-in profiles, which is what the
    VM enforces (S07-egress-gateway, "Enforced per VM"), projected as
    above ("Built-in profiles in the check" below). A failing check
    refuses the session
    start, with the reason, the project and the rule that caused it
    shown and logged. While sessions run, a change that narrows the
    union, so that the new union allows a subset of what the old one
    allowed, applies without this check. Removing a `deny_rules` entry,
    loosening a limit or weakening a mode widens. A change that widens
    and fails the check, or can't be checked, is refused, the VM keeps its
    last policy that passed, and `wb policy explain` shows the change
    as pending (S07-egress-gateway, "Policy changes while sessions
    run"). A recompute when a session ends goes through the same check
    when it widens the union, and on failure the departing project's
    denies, limits, modes and parts that are off stay as a held source
    (S07-egress-gateway, "Session end"). The
    result is cached on the SHA-256
    of the candidate and of the boundary, so an unchanged policy adds
    no prover run to session start (NFR01-startup). The maintainer
    decided on I35 that the user's own policy is checked, because
    approvals are bound by the boundary (B35-openshell-artifacts,
    "Approvals" below).
  - *What the prover models.* At v0.1.2 it compares hosts, ports and
    programs, and method and path rules on `protocol: rest` endpoints
    in `enforce` mode. It answers `unsupported` for GraphQL and
    WebSocket rules, an endpoint in `audit` mode, query matchers, and a
    host and port that has both a REST endpoint and one without rules
    (X24-openshell-artifacts).
  - *Built-in profiles in the check.* The candidate holds the parts of
    the built-in profiles that are on (S07-egress-gateway, "Profile
    parts"), and no built-in rule is left out. The candidate holds the
    git hosting profile on every host that a project's remotes name,
    with the GraphQL path of that host's kind. A host whose kind isn't
    known doesn't add an allow rule until the user sets its kind. An
    organization's boundary limits these rules like any other rule. The default boundary allows every rule
    of every built-in part as the prover sees it.
    - *GraphQL.* The prover can't model GraphQL rules, and the git
      hosting profile has them (S07-egress-gateway). So the candidate
      holds each built-in GraphQL rule as a REST rule that allows
      `POST` to its GraphQL path on its host, such as
      `POST api.github.com/graphql`. That rule allows every operation
      the GraphQL rule allows and more. A candidate inside the
      boundary with the REST rule is inside it with the GraphQL rule
      too.
    - *By source, not by shape.* The default boundary admits that
      projection because it comes from a built-in rule. A user or
      repository rule that can match a request to a built-in host's
      GraphQL endpoint is a load error ("Precedence"), so no other
      source can put a rule of that
      shape in the candidate. The built-in operation filter, which
      denies mutations unless the operation name is allowlisted,
      applies to every request on that endpoint.
    - *The gap.* The boundary then limits whether a host's GraphQL
      endpoint is reachable, not which operations are allowed on it.
      An organization that wants no GraphQL on a host forbids the
      endpoint in its boundary. The check then refuses every session
      start until the user turns that part off, such as the GitHub
      `graphql` part. `wb policy explain` shows each such rule as
      "boundary-checked as POST to its endpoint", and the docs list
      them.
    - *Only built-in rules.* A user or repository rule the prover
      can't model still refuses the policy.
    - *Merge rules.* The merge rules below still hold, so user or
      repository policy can't replace or weaken a built-in rule.

    The maintainer decided this on I35 (B35-openshell-artifacts), in
    the addendum to decision 1, which applies the brief's option C to
    the built-in rules.
- **Precedence.** Built-in defaults → global → project → trusted repo
  config → session approvals. `wb policy explain` shows the effective
  value and its source.
  - *Network rules merge as a union.* Rules are keyed by source and
    rule name, so a later source adds rules and never replaces one. A
    rule in user or repository policy with the name of a built-in
    profile's rule is a load error. Built-in denies apply whatever the
    source of the allow. User and repository policy can't change a
    built-in host's kind, mode or rules. A user or repository rule on
    a built-in host is a load error when its method and path pattern
    can match a request to that host's GraphQL path after
    normalization: a path prefix or glob that covers it, a wildcard
    method, a trailing slash, or percent-encoding. The built-in
    GraphQL operation filter applies whatever the source of the allow.
    The one change the global and
    project policy can make to a built-in profile is to turn a part of
    it off (S07-egress-gateway, "Profile parts"). This lock, and the
    removal of guest credentials in the places a profile names, hold
    whatever parts are on.
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
  guest doesn't get a placeholder for it. A binding on a host that a
  built-in profile covers applies only to the hosts and paths of the
  profile's parts that are on (S07-egress-gateway, "Profile parts").
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

- **Key.** Each CA has its own P-256 key. `wb-proxyd` generates it in
  the platform's hardware key store (Secure Enclave on macOS, TPM
  elsewhere; S12-platforms), non-exportable, self-signs the CA
  certificate with it, and uses it to sign leaf certificates (Go's
  certificate creation accepts any signer). Fallback if no hardware
  key store is available: a software key held in the secret store.
  Only `wb-proxyd` holds the key handle (S04-architecture, "Secrets
  live in one process"). `wb-hostd` gets the public certificate over
  local IPC and hands it to `wb-guestd`.
- **CA profile.** `basicConstraints` critical with `CA:TRUE` and
  `pathLen:0`, `keyUsage` `keyCertSign` only, and no extended key
  usage.
- **Scope.** Each VM has its own CA, and two from day 30 of each CA on
  ("Lifetimes"), so a leaf signed for one VM isn't trusted in another.
  A CA is trusted **only inside guests**, never on the host. The CA
  certificate has no name constraints. The set of inspected hosts
  changes with every approval (S07-egress-gateway, "Approvals and
  learning"), and a constraint that followed it would need a new CA
  each time. Clients that read trust once per process would then
  reject every leaf from the new CA until they restart. The maintainer
  decided this on I39 (B39-ca-rotation).
  - *Leaf issuance.* What limits the names a CA vouches for is
    `wb-proxyd`. It signs a leaf only for the hostname of the stream,
    after the SNI check, and only when the VM's effective policy
    inspects that host (S07-egress-gateway, "Stream path", "Name
    binding" and "Modes"). It signs with the CA of the stream's VM,
    and keys its leaf cache on VM, CA, and hostname. A pass-mode
    stream is relayed unchanged, and `wb-proxyd` doesn't sign a leaf
    for it. Any other stream that fails this rule is reset and logged
    with the rule.
  - *Leaf profile.* Exactly one `dNSName` subject alternative name,
    equal to the stream's hostname. It has neither a wildcard nor an
    IP address name. `CA:FALSE`, extended key usage `serverAuth`, and
    a validity within the signing CA's.
  - *No constraint at all.* A broad constraint that never changes
    limits nothing that leaf issuance doesn't already limit. RFC 5280
    requires the extension to be marked critical, so a client that
    doesn't support it rejects the CA.
  - *Not on the host.* `wb-proxyd`'s pool of roots for upstream
    connections never includes a Wraith Box CA, whatever the host's
    trust store holds.
- **Lifetimes.** Leaf certificates: 24 hours, cut to the signing CA's
  end, cached in memory. CA: 60 days, rotated with an overlap.
  `wb-hostd` schedules each step, and `wb-proxyd` issues, switches and
  destroys. Days count from the current CA's issue:
  - *Day 30.* `wb-proxyd` issues the successor with a new key, and
    `wb-guestd` installs it next to the current CA. `wb-hostd` records
    the install when `wb-guestd` reports it.
  - *Day 58.* `wb-proxyd` signs with the successor, but only once its
    install is recorded. Until then it refuses the VM's inspected
    streams with the rule `ca-not-installed`. A guest that doesn't
    report the install only cuts off its own traffic.
  - *Day 60.* The old CA expires. `wb-guestd` removes it from the trust
    store, and `wb-proxyd` destroys its key. `wb-proxyd` never signs
    with an expired CA.
  - *Overlap.* A process that started before the successor was
    installed works until the switch, 28 days later. Only a process
    that runs longer than that and never rereads its trust fails, with
    a certificate error. Time the VM spends saved counts toward that
    bound.
  - *Boot, restore and host wake.* On every boot, every restore from
    saved state and every host wake, in this order: `wb-guestd` sets
    the guest clock from `wb-hostd` (S06-vm-lifecycle, "Time and
    sleep"), a CA that has expired is removed and its key destroyed, a
    step of the schedule that came due while the VM was off or the host
    slept runs, and if no valid CA remains, `wb-proxyd` issues a new
    one. Then `wb-guestd` installs the VM's current CAs from
    `wb-hostd`'s own record, never from what the guest reports it has,
    and `wb-hostd` records the install. Only then does `wb-proxyd`
    accept the VM's streams. After a restore or a host wake,
    `wb-proxyd` keeps signing with the CA it signed with before the
    save or the sleep, also past day 58, while that CA is valid,
    because resumed processes loaded their trust before it.
  - *Why rotate.* The hardware key can't be read out, so whoever can use
    it controls `wb-proxyd` on the host, and a new certificate doesn't
    change that. Rotation bounds how long the software fallback key
    is useful to someone who copied it, now up to 60 days. Only that
    VM's guest trusts it. The thief also needs a position in that
    guest's traffic, which runs only through the host. With a hardware
    key rotation adds little, and the overlap keeps it from breaking
    running tools. X04-tls-inspection checks which guest clients
    reread trust and whether 28 days of overlap is enough.
  - *Audit.* Issuing a CA, its install, the signing switch, its
    removal, and an install `wb-guestd` refuses or doesn't report are
    each a Device Config State Change (5019) event with the VM and the
    CA certificate's SHA-256 fingerprint (SEC10-audit).
- **Guest trust.** The sealed image doesn't contain a Wraith Box CA
  (S06-vm-lifecycle, "Layers"). `wb-guestd` installs the VM's current
  CAs at runtime in the guest OS's system trust store and sets
  toolchain-specific trust variables so that every common client
  accepts them. Each trust variable's file holds every current CA of the
  VM.

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
  standalone `openshell-prover` binary (X24-openshell-artifacts). So
  the risk check is Go code in `wb-hostd`. It follows the four
  categories and names of OpenShell's check at v0.1.2, adds Wraith
  Box's own checks above, and runs OpenShell's cases as its tests. The
  maintainer decided this on I35 (B35-openshell-artifacts,
  S10-tech-stack).
- **Boundary.** A request whose rule would leave the boundary isn't
  shown. The prover checks the rule alone, in the approved form,
  against the boundary. An approved rule only allows, so a rule inside
  the boundary can't take a policy that is inside it outside. The
  user's own policy is checked at session start for that reason
  ("Boundary check" above). A refusal is logged as a Device Config
  State Change (5019) event. The maintainer decided on I35 that
  approvals are bound by the boundary (B35-openshell-artifacts).
- **Limits.** The guest triggers approval requests, one per unknown
  name it looks up outside the guest OS's background list and the
  hosts of parts that are off (S07-egress-gateway, "Packet path",
  DNS), so prover runs are bounded (SEC13-bounded-resources).
  Requests are deduplicated by host and rate-limited per VM, with a
  limit fixed in code, because the host can't tell which session
  raised one (S07-egress-gateway, "Enforced per VM"). A deny holds
  until the sessions running at the deny have ended, and an answer
  given after the VM's sessions changed is refused and the request
  shown again (S07-egress-gateway, "Deny duration", "Session set at
  the answer"). Each VM has a
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
  SHA-256 of the joining project's policy and of the union, without a
  held source if the VM has one (S07-egress-gateway, "Session end").
  An approval is cached on that pair, so the next start with the same
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
