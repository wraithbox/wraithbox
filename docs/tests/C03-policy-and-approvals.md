# C03 - Policy and Approvals

**Purpose:** The test cases for network policy: loading it, combining
the policies of the projects in one VM, modes per host, profile parts,
learn mode, and the approval flow.

**Verifies:** S07-egress-gateway ("Enforced per VM", "Modes, per
host", "Profile parts", "Approvals and learning", "Session end", "Deny
duration"), S09-01-policy-files ("Mode per host and port"),
S09-03-boundary-check ("Built-in profiles in the check"),
S09-09-approval-flow,
S13-guest-confinement

## Unit tests

### Policy loader

Spec: S07-egress-gateway, "Modes, per host".

- Put a host in pass mode from a repository's `.wraithbox/policy.yaml`
  with `config_trust` on, on a host with a credential binding, on a
  host a built-in profile covers, without a reason, and with a wildcard
  host. Then add a binding on a pass host. See each refused at load
  with an error that names the host and the rule.
- Load a policy with a host that has `_` or a non-ASCII byte in its
  name, and run `wb allow` on such a host, and see both refused with
  the rule `dns-name-form` (S07-egress-gateway, "Packet path", DNS).
- Add a global pass entry on a host that a joining project's remote
  names, and see that project's session start refused, with the project
  named.

## Fuzzing

- Policy matching, and network policy YAML.
- The guest flow labels of S13-guest-confinement.
- The decoder and the script check that show internationalized names in
  approval requests (S09-09-approval-flow).

## Conformance

### The project set and learn mode

- Push to a repository outside the project set, with and without an
  attacker-supplied token. Create a gist or a repository. Publish a
  package. The project set is the VM's: the repositories of every
  project with a session in the VM (S07-egress-gateway, "Enforced per
  VM").
- In a learn-mode session (S07-egress-gateway, "Approvals and
  learning"):
  - resolve a name that isn't allowlisted, and see `NXDOMAIN` with the
    same answer bytes as outside learn mode, no approval event, and the
    name in `wb learn report`;
  - push to a repository outside the project set, and see it refused;
  - resolve more distinct names than the list cap, and see the rest
    refused with `learn-list-full`, not listed, and counted as dropped
    in the summary;
  - resolve a name with a control byte, an escape or a non-ASCII byte,
    and see it refused with `dns-name-form` and absent from the report,
    the summary and `wb status`;
  - put a name with a risk-check finding on the list, approve the
    batch, and see only the names without a finding allowed.

### Per-VM enforcement

Spec: S07-egress-gateway, "Enforced per VM".

- End a project's last session, and see its binding no longer injected,
  its open streams closed with the rule `policy-recomputed`, and its
  project user's processes stopped.
- Give two projects different wildcard budgets or minimum ages, start
  both, and see the stricter one applied to both, including to a count
  reached before the second one joined.
- For every host and port of a union, see the mode `wb-proxyd` applies
  equal the mode the boundary check projected
  (S09-01-policy-files, "Mode per host and port"). With a
  boundary that allows only `GET` on a host:
  - put the host's port 443 in `audit` with a `GET` rule in one
    project, and add an `enforce` endpoint with a `GET` rule on the
    same host and port in another file. See the start pass and a
    `DELETE` to that port refused;
  - then put port 8443 of the same host in `audit` with a `GET` rule
    while port 443 stays in `enforce`, and see the start refused with a
    counterexample that names the host, so a `DELETE` to port 8443 is
    never sent.
- Put port 443 of a host in `audit` in the global policy, with one
  project's `enforce` endpoint with a `GET` rule keeping it in
  `enforce`, and a boundary that allows only `GET`. Remove that
  endpoint while sessions run, and separately end that project's last
  session while another session stays. See each change checked as
  widening and refused by the boundary, the host and port held in
  `enforce` with a 5019 event, and a `DELETE` to it still refused.
- Put `github.com`, a host a project's remote names, or a host with a
  credential binding in `audit`, in a policy file and as a global entry
  loaded before a project names the host. Add a binding on a host in
  `audit` with `wb cred set`. See each refused with the host and the
  rule. Load repository configuration with `enforcement: audit`, and
  see a load error (S07-egress-gateway, "Modes, per host", audit).
- With the prover unavailable, remove a binding while sessions run, and
  see it no longer injected and its streams closed. Then add a rule,
  and separately remove a `deny_rules` entry, and see each change
  refused and shown as pending.
- Start a second project whose policy puts a host in pass mode that the
  first project binds or inspects, whose binding overlaps the first
  one's on the same host, port and path with another secret, or whose
  rules leave the boundary in union. See the start refused with both
  projects named and the first project's policy unchanged.
- With the prover unavailable, end the last session of a project that
  held the stricter minimum age, turned a part off, set inspection on a
  host the other project passes, or had a `deny_rules` entry. See each
  kept as a held source, a 5019 event that names the project, and the
  widening shown as pending, while the project's binding is no longer
  injected (S07-egress-gateway, "Session end").
- Deny a name, start and end another session, and see the name still
  refused with no new approval event until every session running at
  the deny has ended (S07-egress-gateway, "Deny duration").
- Answer an approval after a session started or ended, and see the
  answer refused with `approval-sessions-changed` and the request shown
  again with a new id.

### Sessions that exclude each other

- Start a learn-mode session while a session or a debug shell of
  another project runs, and start another project's session while a
  learn-mode session runs, and see each refused with the other project
  named.
- Start another project, or an `--isolated` session, in the isolated VM
  while a project with `placement = isolated` has a session there, and
  the reverse, and see each refused.
- Start `--learn` in a project with `placement = isolated`, and start
  with `--isolated` in a project with `placement = work` and
  `config_trust` on, and see each refused.

### Profile parts

Spec: S07-egress-gateway, "Profile parts".

- Turn off every GitHub part but `git`, and see:
  - fetch and push to `github.com` pass;
  - any REST or GraphQL request refused with `profile-part-off`;
  - no GitHub binding injected on `api.github.com`;
  - a lookup of `api.github.com` refused at DNS with no approval event,
    also while another project's rule allows that host.
- With the same parts off, load a repository configuration rule on that
  host, or on `api.github.com` with the path `/graphql`, `/graphql/`,
  `/graph*`, `/%67raphql` or `/**` and any method, and see a load
  error.
- With a boundary that forbids `api.github.com` and only the GitHub
  `graphql` part on, see the prover refuse the session start
  (S09-03-boundary-check, "Built-in profiles in the check").

### Approvals

Spec: S09-09-approval-flow.

- Look up a burst of unknown hostnames from the guest, and see the
  approval limits deny the requests past the limit and log each with
  the rule `approval-pending-cap` or `approval-rate`, `wb status` show
  the counts, and no more prover runs than the cap.
- Leave a request unanswered, end every session it records, and see it
  expire with the rule `approval-expired` and grant nothing.
- Look up an `xn--` name whose Unicode form mixes Latin and Cyrillic,
  and one that fails to decode, and see each request show the ASCII
  form with a warning. Look up `xn--bcher1-3ya.de` (`bücher1.de`), and
  see no warning.
- Answer a request with `wb approve <id>` after it expired, and after
  it was replaced when the VM's sessions changed. See each answer
  refused with `approval-stale`, and the newer request for the same
  name still pending, its old notification withdrawn.
- With one request pending, run `wb approve` without an id, and see it
  refused with the pending id listed.
- With 16 requests pending, restart `wb-netd`, look up a 17th unknown
  name, and see it refused with `approval-pending-cap`.
- From a test `wb-netd` that skips its own check, send a 17th approval
  event, and see `wb-hostd` drop it without a notification, logged with
  the same rule.

### Flow labels

Spec: S13-guest-confinement.

Once X14-flow-attribution has delivered labels:

- Forge or omit a flow label, and see only rules without program
  narrowing match.
- Replace a pinned binary, and see its connections denied.

**Status:** Draft
