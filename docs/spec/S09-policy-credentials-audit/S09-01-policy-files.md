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

Wraith Box extensions go in separate top-level keys:

- per-host inspect or pass mode;
- raw TCP entries;
- the wildcard budget;
- dependency-gate thresholds;
- credential bindings.

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
S07-05-hosts-and-modes, "Modes, per host", has the rules.

#### Raw TCP entries

A raw TCP entry names one exact host and one port, for a stream that is
neither TLS nor HTTP. The rules of a pass host apply to it, on its host
and port. S07-10-non-http-streams has the rules.

#### Audit hosts

Only the global and project network policy files can put a host and
port in `audit`.

An `audit` endpoint on a host with a credential binding, or on a host a
built-in profile covers, is refused (SEC06-repo-writes):

- at load;
- at every recompute;
- when a binding is added later.

S07-05-hosts-and-modes, "Modes, per host", has the rules.

### Policy as the prover sees it

#### What the prover sees

The document Wraith Box writes for the prover allows at least what
`wb-proxyd` enforces, never less.

- A built-in GraphQL rule is written as a REST rule. It allows `POST`
  to its GraphQL path on its host, which is wider than the rule
  (S09-03-boundary-check, "Built-in profiles in the check").
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
(S07-05-hosts-and-modes, "Modes, per host"). So a host and port whose
mode resolves to `audit` (S09-11-precedence, "Mode per host and port") is written as the whole host on that port.

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
