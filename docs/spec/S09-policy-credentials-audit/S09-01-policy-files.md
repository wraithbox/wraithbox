# S09-01 - Policy Files

**Purpose:** Where policy is written, the network policy schema Wraith Box
accepts, how the prover sees it, and narrowing by program and project.

## Design

### Files

Policy is in two kinds of files. Both are on the host, outside every
repository and every guest. Both are the same on every host OS
(FR17-same-everywhere).

- *Settings* in TOML: `<config>/config.toml` and
  `<config>/projects/<project-id>.toml`.
- *Network policy* in YAML: `<config>/policy.yaml` and
  `<config>/projects/<project-id>.policy.yaml`.

`<config>` is per OS in S12-platforms (`~/.config/wraithbox` on
macOS).

### Network policy schema

Network policy uses the `network_policies` part of OpenShell's policy
schema (version 1). It holds named rules of endpoints and `binaries`.
An endpoint has a host, port, path, protocol, an access preset or
method and path rules, and `enforce` or `audit`.

Wraith Box parses and validates the schema in Go, with its own code.

#### Extension keys

Wraith Box's extensions go under one top-level key, `wraithbox`:
`pass` (host, reason), `raw_tcp` (host, port, reason),
`wildcard_budget` (S07-03-dns) and `parts_off` (profile, kind, part:
S07-11-http-policy, "Profile parts"). `credential_bindings`,
`dependency_gate` and `git_hosts` aren't built yet, so they are refused
with `policy-key-unsupported`. Repository configuration can't hold
`wraithbox` (`policy-source`, S09-03-project-settings, "Closed keys").

#### Pinned release

OpenShell adds fields to the schema without changing `version: 1`. So
the pin is an OpenShell release: v0.1.2 (X24-openshell-artifacts,
S10-tech-stack). Wraith Box accepts the keys of that release only.

#### Closed keys

OpenShell rejects a policy with an unknown key at any level. So a file
with Wraith Box's extension keys isn't a valid OpenShell policy
(X24-openshell-artifacts).

Wraith Box keeps the extensions in the same file. When it hands policy
to OpenShell's tooling, it writes a new document with only `version`
and `network_policies`. It never hands over the user's file.

#### Implemented keys

Per endpoint, Wraith Box implements `host`, `port` or `ports`, `path`,
`protocol`, `enforcement`, `access`, `rules` and `deny_rules`.

- `protocol` is `rest` or absent. An endpoint without one is L4: it
  allows every method and path on its host and port, still inspected.
  Any other protocol, OpenShell's `tcp` included, is refused with
  `policy-protocol-unsupported` (I489, maintainer decision 1).
- An endpoint sets `port` or `ports`, not both. OpenShell takes
  `ports` when both are set, so both is refused with `policy-port`.
- A rule's name is OpenShell's effective name: its `name` when it has
  one, and its key otherwise. A name that another rule in the file has,
  and a name of the built-in profiles' rules, are refused with
  `policy-rule-name`. The built-in names are the profile names and
  every name that starts with `builtin`, in any case.
- A rule that names the method `CONNECT`, in `rules` or `deny_rules`,
  is refused with `http-tunnel` (S07-16-tls-and-tunnels).

The built-in profiles need `deny_rules`, for denies such as the git
hosting profile's archive deny. The prover's handling of `deny_rules`
wasn't tested (X24-openshell-artifacts). It is tested before the
boundary check relies on it.

#### Refused keys

Any other OpenShell key is refused at load, with an error that names it
(NFR06-explained-refusals). It is never ignored, so no rule is weaker
than written. Examples:

- the other top-level keys, `filesystem_policy` and
  `network_middlewares`;
- the endpoint fields `credential_binding`, `mcp` and `allowed_ips`.

`allowed_ips` would allow destinations by raw IP address
(SEC05-default-deny).

#### Strings

Every rule name, host, path and method must:

- be ASCII;
- have no control bytes;
- be within a length cap fixed in code.

Otherwise the file is refused at load. The prover models ASCII values
only, and its output repeats these strings (X24-openshell-artifacts).

#### Loading

`wb-hostd` loads each file with its own Go code
(`internal/policy/policyfile`). A repository's file is repository
data, so every byte is treated as hostile. The prover runs confined
because it parses the same bytes (S09-04-boundary-check). The loader
stays in `wb-hostd` because it is Go, bounded and fuzzed
(`FuzzLoadPolicy`, C03-policy-and-approvals).

- A file past OpenShell's own bounds is refused with
  `policy-yaml-bound`: 4 MiB, checked before parsing, depth 64 and
  100,000 nodes (X24-openshell-artifacts). The depth and nodes are
  counted on the node tree, so the decoder builds the whole tree of a
  file within 4 MiB first.
- An integer is decimal with no leading zero, because YAML reads `0443`
  as octal. A panic in the loader refuses the file with
  `policy-loader-panic`.
- The YAML is decoded into a node tree first, which keeps an alias as
  a pointer and expands nothing. An anchor, an alias, a merge key, a
  tag outside YAML's core schema, a duplicate key, a key that isn't a
  string and a second document are refused with `policy-yaml-form`.
- Every level is then checked against its closed key list before any
  value is read: `policy-key-unknown`, `policy-key-refused` and
  `policy-key-unsupported`. `version` is 1 (`policy-version`).
- Each refusal names its rule, the file, the line and key path, and the
  value at fault, escaped (NFR06-explained-refusals). One load reports
  the first refusal of each rule, at most 16.

### Pass, raw TCP, and audit entries

#### Pass hosts

Only the global and project network policy files can put a host in
pass mode, never repository configuration. Each pass entry names one
exact host, and needs a reason. The reason follows the string rules
("Strings").

These are refused at load:

- a wildcard entry;
- an entry for a host with a credential binding;
- an entry for a host a built-in profile covers.

Adding a pass host is a Device Config State Change (5019) event.
S07-07-hosts-and-modes, "Modes, per host", has the rules.

#### Raw TCP entries

A raw TCP entry names one exact host and one port, for a stream that is
neither TLS nor HTTP. The rules of a pass host apply to it, on its host
and port. S07-15-non-http-streams has the rules.

#### Audit hosts

Only the global and project network policy files can put a host and
port in `audit`.

An `audit` endpoint on a host with a credential binding, or on a host a
built-in profile covers, is refused (SEC06-repo-writes):

- at load;
- at every recompute;
- when a binding is added later.

S07-07-hosts-and-modes, "Modes, per host", has the rules.

### Policy as the prover sees it

#### What the prover sees

The document Wraith Box writes for the prover allows at least what
`wb-proxyd` enforces, never less.

- A built-in GraphQL rule is written as a REST rule. It allows `POST`
  to its GraphQL path on its host, which is wider than the rule
  (S09-04-boundary-check, "Built-in profiles in the check").
- A host in pass mode is written as an L4 endpoint, with no `protocol`
  and no rules, whatever rules the file gives it. So the boundary must
  allow L4 to that host.
- A raw TCP entry is written as an L4 endpoint for its host and port.
  So the boundary must allow L4 to that host and port.

The prover can't see the extension keys. Wraith Box's own Go check
compares them with the boundary's extension part:

- each pass host must be a pass host in the boundary;
- each raw TCP entry must be a raw TCP entry in the boundary;
- dependency-gate thresholds can't be looser;
- the wildcard budget can't be larger.

Any mismatch refuses the policy.

#### Audit as the whole host

`audit` lets every request through, and only logs a violation
(S07-07-hosts-and-modes, "Modes, per host"). So a host and port whose
mode resolves to `audit` (S09-02-precedence, "Mode per host and port") is written as the whole host on that port.

The projected endpoint is built from the host and the port only:

- `protocol: rest` and `enforcement: enforce`;
- one rule, `allow: { method: "*", path: "/**" }`;
- in an entry whose `binaries` is `path: "/**"`.

These are dropped: the endpoint-level `path`, `access` and `rules`, the
entry's `binaries`, and the `deny_rules` of every endpoint on that host
and port.

An endpoint with `ports` is written once for each port, each in the
mode its host and port resolves to. A host and port that resolves to
`enforce` is written with its rules.

The boundary must then allow every method and path on that host and
port. A boundary that allows less refuses the policy, with a
counterexample that names the host.

The prover's reading of `method: "*"` wasn't tested
(X24-openshell-artifacts). It is tested before the boundary check
relies on it.

### Program narrowing

`binaries` entries match the program label from the guest
(S13-guest-confinement). Until spike X14-flow-attribution has delivered
labels, `binaries` does not narrow a rule.

- Each rule lists the universal entry `path: "/**"`.
- Policy that names programs is rejected at load, with an error saying
  why (NFR06-explained-refusals). So no rule is silently weaker than
  written.

OpenShell's policy engine has the same mode, switched by its
`require_binary_identity` setting. Its isolation backends must resolve
binary identity, though (X24-openshell-artifacts).

The prover checks each policy both with and without binary identity.
With it, the prover reads `/**` as every program
(X24-openshell-artifacts).

### Project narrowing

A project's rules and bindings apply to the whole VM while the project
has a session in it (S07-01-per-vm-policy).

X14-flow-attribution may deliver labels that name the project user
(S13-guest-confinement). Once it does, those labels may narrow a
project's rules to its own flows. The rules of program narrowing then
apply: a missing or malformed label matches no narrowed rule.

Until then nothing is narrowed by project.

### The effective policy on the wire

`wb-hostd` merges the files into one effective policy per VM
(S09-02-precedence). It reaches `wb-netd` and `wb-proxyd` as a
`wraithbox.policy.v1.EffectivePolicy` message
(`proto/wraithbox/policy/v1/policy.proto`).

- Each rule names its source: built-in, global, project, repository
  or approval.
- Hosts are in canonical form, and path patterns as written.
- The proto file names a bound for every list and string.
- The receiver checks the message before it uses it. A message past a
  bound, or one that breaks a rule of this page, is refused whole and
  never repaired. The refusal is logged with its rule
  (NFR06-explained-refusals).

## Decisions

### Reuse OpenShell's network policy schema

- **Why:** OpenShell's prover can then check Wraith Box policy, and
  users get one policy language for both tools.

### Only global and project files set pass hosts

- **Decided:** I40 (B40-learn-pass-modes).

### Raw TCP entries follow the pass-host rules

- **Decided:** I47 (B47-non-http-streams).

### `audit` only from global and project files, never on guarded hosts

- **Decided:** I109.

### `audit` is projected as the whole host

- **Decided:** I109.

**Status:** Draft
