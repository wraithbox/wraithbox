# S09-01 - Policy Files

**Purpose:** Where policy is written, the network policy schema Wraith Box
accepts, narrowing by program and project, and how the sources merge.

## Design

### Files

Two kinds, both on the host, outside every repository and
every guest, and the same on every host OS (FR17-same-everywhere). `<config>` is per OS
in S12-platforms (`~/.config/wraithbox` on macOS).
- *Settings* in TOML: `<config>/config.toml` and
  `<config>/projects/<project-id>.toml`.
- *Network policy* in YAML: `<config>/policy.yaml` and
  `<config>/projects/<project-id>.policy.yaml`.

### Network policy schema

The `network_policies` part of OpenShell's
policy schema (version 1): named rules of endpoints (host, port, path,
protocol, access preset or method and path rules, `enforce` or
`audit`) and `binaries`. Wraith Box parses and validates it in Go with
its own code. Reusing the schema lets OpenShell's prover check Wraith
Box policy, and gives users one policy language for both tools.
Wraith Box extensions (per-host inspect or pass mode, raw TCP
entries, wildcard budget, dependency-gate thresholds, credential
bindings) go in
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

### Pass, raw TCP, and audit entries

- *Pass hosts.* Only the global and project network policy files can
  put a host in pass mode, never repository configuration. Each pass
  entry names one exact host and needs a reason, which follows the
  string rules above. A wildcard entry, or an entry for a host with a
  credential binding or one a built-in profile covers, is refused at
  load, and adding a pass host is a Device Config State Change (5019)
  event. The maintainer decided this
  on I40 (B40-learn-pass-modes), and S07-05-hosts-and-modes, "Modes, per host", has the rules.
- *Raw TCP entries.* A raw TCP entry names one exact host and one
  port for a stream that is neither TLS nor HTTP. The rules of a
  pass host above apply to it, on its host and port. The
  maintainer decided this on I47 (B47-non-http-streams), and
  S07-10-non-http-streams, has the rules.
- *Audit hosts.* Only the global and project network policy files
  can put a host and port in `audit`. An `audit` endpoint on a host
  with a credential binding, or on one a built-in profile covers, is
  refused at load, at every recompute, and when a binding is added
  later (SEC06-repo-writes). The maintainer decided this on I109, and
  S07-05-hosts-and-modes, "Modes, per host", has the rules.

### Policy as the prover sees it

- *What the prover sees.* The document Wraith Box writes for the
  prover allows at least what `wb-proxyd` enforces, never less. A
  built-in GraphQL rule is written as a REST rule that allows `POST`
  to its GraphQL path on its host, which is wider than the rule
  (S09-03-boundary-check, "Built-in profiles in the check"). A host in pass mode is written
  as an L4 endpoint with no `protocol` and no rules, whatever rules
  the file gives it, so the boundary must allow L4 to that host. A
  raw TCP entry is written as an L4 endpoint for its host and port,
  so the boundary must allow L4 to that host and port.
  The prover can't see the extension keys, so Wraith Box's own Go
  check compares them with the
  boundary's extension part: each pass host must be a pass host in
  the boundary, each raw TCP entry must be a raw TCP entry in the
  boundary, dependency-gate thresholds can't be looser, and the
  wildcard budget can't be larger. Any mismatch refuses the policy.
- *Audit as the whole host.* `audit` lets every request through and
  only logs a violation (S07-05-hosts-and-modes, "Modes, per host"), so
  a host and port whose mode resolves to `audit` ("Precedence",
  "Mode per host and port") is written as the whole host on that
  port. The projected endpoint is built from the host and the port
  only: `protocol: rest`, `enforcement: enforce`, and one rule
  `allow: { method: "*", path: "/**" }`, in an entry whose
  `binaries` is `path: "/**"`. The endpoint-level `path`, `access`
  and `rules`, the entry's `binaries`, and the `deny_rules` of every
  endpoint on that host and port are dropped. An endpoint with
  `ports` is written once for each port, each in the mode its host
  and port resolves to. The boundary must then allow every method
  and path on that host and port, and a boundary that allows less
  refuses the policy with a counterexample that names the host. The
  prover's reading of `method: "*"` wasn't tested
  (X24-openshell-artifacts), and is tested before the boundary check
  relies on it. A host and port that resolves to `enforce` is
  written with its rules. The maintainer decided this on I109.

### Program narrowing

`binaries` entries match the program label
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

### Project narrowing

A project's rules and bindings apply to the
whole VM while the project has a session in it (S07-01-per-vm-policy). Once X14-flow-attribution delivers labels that
name the project user (S13-guest-confinement), those labels may
narrow a project's rules to its own flows. The rules of program
narrowing apply: a missing or malformed label matches no narrowed
rule. Until then nothing is narrowed by project.

### Precedence

Built-in defaults → global → project → repository
configuration (with `config_trust`) → session approvals. `wb policy explain` shows the effective
value and its source.
- *Network rules merge as a union.* Rules are keyed by source and
  rule name, so a later source adds rules and never replaces one. A
  rule in user or repository policy with the name of a built-in
  profile's rule is a load error. Built-in denies apply whatever the
  source of the allow, and are evaluated before any allow. User and repository policy can't change a
  built-in host's kind, mode or rules. A user or repository rule on
  a built-in host is a load error when its method and path pattern
  can match a request to that host's GraphQL path after
  normalization: a path prefix or glob that covers it, a wildcard
  method, a trailing slash, or percent-encoding. The built-in
  GraphQL operation filter applies whatever the source of the allow.
  The one change the global and
  project policy can make to a built-in profile is to turn a part of
  it off (S07-07-http-policy, "Profile parts"). This lock, and the
  removal of guest credentials in the places a profile names, hold
  whatever parts are on.
- *Mode per host and port.* `enforcement` is resolved for each host
  and port over the whole union, not per endpoint: the built-in
  profiles, the global, project and repository policy, the approved
  rules, every endpoint within one file, and every project with a
  session in the VM ("Per VM"). Any endpoint in `enforce` on a host
  and port, from any source, puts that host and port in `enforce`,
  and an endpoint with no `enforcement` field counts as `enforce`.
  An endpoint counts on every host its host pattern matches, so a
  wildcard endpoint in `enforce` keeps a matching exact host in
  `enforce`, which fails closed.
  A host and port resolves to `audit` only when every endpoint on it
  is in `audit`. `wb-proxyd`
  applies the resolved mode to every request on that host and port,
  never the `enforcement` field of the endpoint whose rule matched,
  and the boundary check projects the same mode ("Audit as the
  whole host"). A change that moves a host and port from `enforce`
  to `audit`, whatever removal caused it, widens
  (S07-01-per-vm-policy, "Policy changes while sessions
  run", "Session end"). The maintainer decided this on I109.
- *Per VM.* The host enforces the merge for a VM, not for a project:
  the project sources are those of every project with a session in
  the VM. Limits take the strictest value among them, and the mode
  of a host and port resolves over every source, so `enforce` wins
  over `audit` ("Mode per host and port"). A conflict between projects, such as a host
  in pass mode for one and inspected or bound for another, or two
  bindings for one host and path, refuses the joining session (S07-01-per-vm-policy). The extension check above runs over the union at each
  recompute. `wb policy explain` shows for each rule and each
  credential binding the project it comes from and the projects
  whose sessions can reach it now, which are all the projects with a
  session in the same VM (T11-shared-vm-grants).

**Status:** Draft
