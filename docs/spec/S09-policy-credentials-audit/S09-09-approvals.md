# S09-09 - Approvals

**Purpose:** What an approval adds, the checks that each request goes
through, and approvals for a joining project.

**Requirements:** SEC14-no-fake-approvals

## Design

Before an approval request is shown, the rule it would add goes through
a risk check. The check compares the policy with and without the rule,
and reports what the rule adds. Findings are part of the request.

Only the user approves a request.

### Approved rule

An approval adds one rule of a fixed form:

- one exact host, with no wildcard;
- `protocol: rest`;
- `enforcement: enforce`;
- `access: read-only` (GET, HEAD and OPTIONS);
- `binaries: /**`.

A write method needs a separate request (S07-17-approvals-and-learning,
"What an approval grants"). It adds a
rule of the same fixed form, with one named method and the exact path,
or path prefix, that the request used, in place of `access: read-only`.

Both forms only allow: no denies, no `audit` mode, no extension keys.

`wb allow` adds only a rule of the first form. It never adds a pass
host or a raw TCP entry (S07-15-non-http-streams).

### Host name

The host in an approved rule comes from the guest. It must pass the
same ASCII, control-byte and length rules as policy strings before the
risk check or the prover sees it. Otherwise the request stays denied,
and is logged with the rule.

### Checks

Each check reports under a name. The first four take their names from
OpenShell's proposal risk check.

- `link_local_reach` (OpenShell): the rule reaches a link-local or
  cloud metadata address.
- `credential_reach_expansion` (OpenShell): the rule gives a host and
  port with a credential binding reach it didn't have.
- `capability_expansion` (OpenShell): the rule adds a method on a host
  that already had reach with a credential.
- `l7_bypass_credentialed` (OpenShell's name, Wraith Box's meaning):
  the rule puts a host with a credential binding into pass mode, or
  gives it an L4-only endpoint.
- Wraith Box only: a first write method on a host without a credential
  (S07-17-approvals-and-learning, "What an approval grants").
- Wraith Box only: a shared object-storage host or a wildcard over one,
  and signature parameters on a write method (X22-no-guest-credentials,
  and S07-08-requests-and-credentials, "Signed URLs").
- Wraith Box only: a host that mirrors a gated registry
  (S07-12-dependency-gate, the condition on which T07-ungated-sources
  was accepted).

#### Why `l7_bypass_credentialed` matters

`wb-proxyd` injects credentials only into inspected HTTP. So no program
that doesn't speak HTTP gets a credential, and this is the bypass that
matters.

OpenShell keys this check on its list of known programs. It never
reports a `/**` rule (X24-openshell-artifacts).

### Source

The risk check is Go code in `wb-hostd`. It:

- follows the four categories and names of OpenShell's check at
  v0.1.2;
- adds Wraith Box's own checks ("Checks");
- runs OpenShell's cases as its tests.

### Boundary

A request whose rule would leave the boundary isn't shown.

The prover checks the rule alone, in the approved form, against the
boundary. An approved rule only allows. So a rule inside the boundary
can't take a policy that is inside it outside. The user's own policy is
checked at session start for that reason (S09-04-boundary-check).

A refusal is logged as a Device Config State Change (5019) event.

### Limits

The guest triggers approval requests: one per unknown name it looks
up, outside the guest OS's background list and the hosts of parts
that are off (S07-03-dns). So prover runs are bounded
(SEC13-bounded-resources).

- Requests are deduplicated by host.
- Each VM has limits on pending and new requests
  (S09-10-approval-flow).
- A deny holds until the sessions running at the deny have ended.
- An answer given after the VM's sessions changed is refused. The
  request is shown again as a new request (S09-10-approval-flow, and
  S07-04-dns-refusals, "Deny duration", "Session set at the answer").
- Each VM has a cap on concurrent prover runs (S04-architecture).
- Boundary results are cached per host, port and rule form, keyed on
  the SHA-256 of the boundary document. So a changed boundary is
  checked again.

A request over a limit stays denied, and is logged with the rule that
limited it.

### Failure

A request whose risk check or boundary check fails or can't run can't
be approved. The destination stays denied, and the failure is logged.

### Join approval

The risk check can find something in a joining project's rules and
bindings, compared with the VM's current union (S07-01-per-vm-policy).
The session start then waits on a join approval.

It is delivered like any approval request. It lists:

- the joining project;
- the projects already in the VM;
- each finding.

The answer is a Device Config State Change (5019) event. It records:

- the projects;
- the findings;
- the decision;
- the SHA-256 of the joining project's policy and of the union, without
  a held source if the VM has one (S07-01-per-vm-policy, "Session end").

An approval is cached on that pair. So the next start with the same
policy and the same union doesn't ask again. Any change to either asks
again.

A denial, or no answer before the request expires, refuses the start.

#### Expiry

The set a join approval records holds the sessions in the VM and the
waiting `wb` process. The request expires:

- when that `wb` exits;
- when the VM's set of sessions changes before the answer.

The expiry is a Device Config State Change (5019) event, with the rule
`approval-expired`, and the start is refused.

### Delivery

Approval requests are delivered:

- as native notifications, through the platform's notification helper
  (S12-platforms);
- through `wb approve` / `wb deny`.

They are never written to the terminal stream of an agent session,
which the guest controls.

Guest-influenced text in a request is stripped of control and escape
sequences wherever it is shown.

## Decisions

### Wraith Box's own risk check, in Go

- **Why:** OpenShell's check can't be run as it is. It is a Rust
  library API that OpenShell's gateway calls. It isn't in the
  standalone `openshell-prover` binary (X24-openshell-artifacts).
- **Decided:** I35 (B35-openshell-artifacts, S10-tech-stack).

### Approvals are bound by the boundary

- **Decided:** I35 (B35-openshell-artifacts).

**Status:** Draft
