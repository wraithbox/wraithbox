# S09-03 - Boundary Check

**Purpose:** The check of a project's policy against the boundary policy
with OpenShell's prover, and which policy it checks when.

## Design

### Boundary check

The boundary policy is the most a project may ever be allowed. The
merged project policy is checked against it with OpenShell's prover:

- at `wb trust`;
- whenever the repository configuration of a project with
  `config_trust` changes.

The prover runs as an external program (S10-tech-stack):
`openshell-prover check --output json`.

A policy is accepted only when the result is `within_boundary` and
`coverage.domains` lists `network_l4` and `network_rest`.

- A policy that exceeds the boundary is refused, and the prover's
  counterexample is shown.
- Every other result refuses the policy too: `unsupported`,
  `inconclusive`, `error`, or a missing or unknown field. The prover's
  `reason_code` and reason are shown and logged.

The default boundary ships with Wraith Box. An organization may supply
its own.

#### How the prover runs

The prover parses repository bytes, so it doesn't run with
`wb-hostd`'s access.

1. `wb-hostd` writes the candidate and the boundary to two documents.
2. `wb-hostd` starts `wb-prover` through `wb-launcher`
   (S04-architecture).
3. `wb-prover` sets its memory cap on itself, before it runs the
   prover, because `wb-hostd` isn't its parent.
4. `wb-prover` confines itself to those descriptors, with no network.
5. `wb-prover` runs the prover with fixed arguments.

The two documents and the pipe for the result are inherited
descriptors, never paths in the request. So `wb-prover` adds no
exception to the launcher's "no arguments" rule. The prover reads them
as `/dev/fd/N`, or `wb-prover` copies them into its own confined
temporary directory first.

`wb-hostd` kills `wb-prover` past a wall-clock limit. A kill or any
non-zero exit refuses the policy (SEC12-least-privilege,
SEC13-bounded-resources).

#### Which binary

The prover comes from the installed bundle, re-signed with Wraith Box's
signing identity at release.

Before it runs the prover, `wb-prover` checks the binary's SHA-256
against the value pinned at release. A mismatch refuses the policy, and
is a Detection Finding (2004) event.

The release checks OpenShell's build attestation, not the run
(S10-tech-stack).

#### Output

`wb trust` shows the prover's reason and counterexample stripped of
control and escape sequences, as approval text is.

### The user's own policy

#### User policy

The user's global and project network rules are checked against the
boundary too. That happens at session start and whenever they change,
also for a project without `config_trust`.

The candidate is what the VM enforces (S07-01-per-vm-policy):

- the global rules;
- the rules of every project with a session in the VM;
- the built-in profiles.

It is projected the same way ("Built-in profiles in the check"
below).

A failing check refuses the session start. The reason, the project and
the rule that caused it are shown and logged.

#### Changes while sessions run

A change that narrows the union applies without this check. Narrowing
means the new union allows a subset of what the old one allowed.

These widen:

- removing a `deny_rules` entry;
- loosening a limit;
- weakening a mode;
- any change that moves a host and port from `enforce` to `audit`,
  such as removing the last `enforce` endpoint on it
  (S09-11-precedence, "Mode per host and port").

A change that widens and fails the check, or can't be checked, is
refused. The VM then keeps its last policy that passed, and
`wb policy explain` shows the change as pending (S07-01-per-vm-policy,
"Policy changes while sessions run").

A recompute when a session ends goes through the same check when it
widens the union. On failure, the departing project's denies, limits,
modes and parts that are off stay as a held source
(S07-01-per-vm-policy, "Session end").

#### Cache

The result is cached on the SHA-256 of the candidate and of the
boundary. So an unchanged policy adds no prover run to session start
(NFR01-startup).

### What the prover covers

#### What the prover models

At v0.1.2 the prover compares hosts, ports and programs, and method and
path rules on `protocol: rest` endpoints in `enforce` mode.

It answers `unsupported` for:

- GraphQL and WebSocket rules;
- query matchers;
- a host and port that has both a REST endpoint and one without rules
  (X24-openshell-artifacts);
- an endpoint in `audit` mode.

The candidate never holds an endpoint in `audit` mode. Wraith Box
writes it as the whole host in `enforce` mode (S09-01-policy-files,
"Audit as the whole host").

#### Built-in profiles in the check

The candidate holds the parts of the built-in profiles that are on
(S07-07-http-policy, "Profile parts"). No built-in rule is left out.

- The candidate holds the git hosting profile on every host that a
  project's remotes name, with the GraphQL path of that host's kind.
- A host whose kind isn't known doesn't add an allow rule until the
  user sets its kind.

An organization's boundary limits these rules like any other rule. The
default boundary allows every rule of every built-in part as the prover
sees it.

##### GraphQL

The prover can't model GraphQL rules, and the git hosting profile has
them (S07-egress-gateway). So the candidate holds each built-in GraphQL
rule as a REST rule. That rule allows `POST` to its GraphQL path on its
host, such as `POST api.github.com/graphql`.

The REST rule allows every operation the GraphQL rule allows, and more.
So a candidate inside the boundary with the REST rule is inside it with
the GraphQL rule too.

##### By source, not by shape

The default boundary admits that projection because it comes from a
built-in rule.

A user or repository rule that can match a request to a built-in
host's GraphQL endpoint is a load error (S09-11-precedence). So no other source can put a rule of that shape in the
candidate.

The built-in operation filter applies to every request on that
endpoint. It denies mutations unless the operation name is
allowlisted.

##### The gap

The boundary then limits whether a host's GraphQL endpoint is
reachable, not which operations are allowed on it.

An organization that wants no GraphQL on a host forbids the endpoint in
its boundary. The check then refuses every session start until the user
turns that part off, such as the GitHub `graphql` part.

`wb policy explain` shows each such rule as "boundary-checked as POST
to its endpoint", and the docs list them.

##### Only built-in rules

A user or repository rule the prover can't model still refuses the
policy.

##### Merge rules

The merge rules still hold (S09-11-precedence). So user
or repository policy can't replace or weaken a built-in rule.

## Decisions

### Check the user's own policy too

- **Why:** approvals are bound by the boundary (S09-08-approvals).
- **Decided:** I35 (B35-openshell-artifacts).

### Built-in GraphQL rules are checked as `POST` to their endpoint

The brief's option C, applied to the built-in rules.

- **Decided:** I35 (B35-openshell-artifacts), in the addendum to
  decision 1.

**Status:** Draft
