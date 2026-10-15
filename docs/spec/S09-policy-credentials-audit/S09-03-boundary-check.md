# S09-03 - Boundary Check

**Purpose:** The check of a project's policy against the boundary policy
with OpenShell's prover, and which policy it checks when.

## Design

### Boundary check

At `wb trust` and whenever the repository
configuration of a project with `config_trust` changes, the merged project policy is checked against a
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

### The user's own policy

- *User policy.* The user's global and project network rules are
  checked against the boundary too, at session start and whenever
  they change, also for a project without `config_trust`. The
  candidate is the global rules and the rules of every project with
  a session in the VM, with the built-in profiles, which is what the
  VM enforces (S07-egress-gateway, "Enforced per VM"), projected as
  above ("Built-in profiles in the check" below). A failing check
  refuses the session
  start, with the reason, the project and the rule that caused it
  shown and logged. While sessions run, a change that narrows the
  union, so that the new union allows a subset of what the old one
  allowed, applies without this check. Removing a `deny_rules` entry,
  loosening a limit or weakening a mode widens, and so does any
  change that moves a host and port from `enforce` to `audit`, such
  as removing the last `enforce` endpoint on it (S09-01-policy-files, "Mode per host and port"). A change that widens
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
  S09-08-approvals).

### What the prover covers

- *What the prover models.* At v0.1.2 it compares hosts, ports and
  programs, and method and path rules on `protocol: rest` endpoints
  in `enforce` mode. It answers `unsupported` for GraphQL and
  WebSocket rules, query matchers, and a host and port that has both
  a REST endpoint and one without rules (X24-openshell-artifacts). It
  answers `unsupported` for an endpoint in `audit` mode too. The
  candidate never holds one, because Wraith Box writes it as the
  whole host in `enforce` mode (S09-01-policy-files, "Audit as the whole host").
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
    GraphQL endpoint is a load error (S09-01-policy-files, "Precedence"), so no other
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
  - *Merge rules.* The merge rules still hold (S09-01-policy-files, "Precedence"), so user or repository policy can't replace or weaken
    a built-in rule.

  The maintainer decided this on I35 (B35-openshell-artifacts), in
  the addendum to decision 1, which applies the brief's option C to
  the built-in rules.

**Status:** Draft
