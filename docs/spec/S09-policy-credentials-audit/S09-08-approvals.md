# S09-08 - Approvals

**Purpose:** What an approval adds, the checks that each request goes
through, and approvals for a joining project.

**Requirements:** SEC14-no-fake-approvals

## Design

Before an approval request is shown, the rule it would add goes
through a risk check, which compares the policy with and without the
rule and reports what the rule adds. Findings are part of the request.
Only the user approves a request.

### Approved rule

An approval adds one rule of a fixed form: one
exact host with no wildcard, `protocol: rest`, `enforcement: enforce`,
`access: read-only` (GET, HEAD and OPTIONS) and `binaries: /**`. A
write method needs a separate request (S07-egress-gateway), which
adds a rule of the same fixed form with one named method and the
exact path, or path prefix, the request used in place of
`access: read-only`. Both forms only allow: no denies, no `audit`
mode, no extension keys. `wb allow` adds only a rule of the first
form, never a pass host or a raw TCP entry (S07-egress-gateway,
"Non-HTTP streams").

### Host name

The host in an approved rule comes from the guest. It
must pass the same ASCII, control-byte and length rules as policy
strings before the risk check or the prover sees it, or the request
stays denied and is logged with the rule.

### Checks

Each check reports under a name. The first four take
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

### Source

OpenShell's check can't be run as it is. It is a Rust
library API that OpenShell's gateway calls, and isn't in the
standalone `openshell-prover` binary (X24-openshell-artifacts). So
the risk check is Go code in `wb-hostd`. It follows the four
categories and names of OpenShell's check at v0.1.2, adds Wraith
Box's own checks above, and runs OpenShell's cases as its tests. The
maintainer decided this on I35 (B35-openshell-artifacts,
S10-tech-stack).

### Boundary

A request whose rule would leave the boundary isn't
shown. The prover checks the rule alone, in the approved form,
against the boundary. An approved rule only allows, so a rule inside
the boundary can't take a policy that is inside it outside. The
user's own policy is checked at session start for that reason
(S09-03-boundary-check). A refusal is logged as a Device Config
State Change (5019) event. The maintainer decided on I35 that
approvals are bound by the boundary (B35-openshell-artifacts).

### Limits

The guest triggers approval requests, one per unknown
name it looks up outside the guest OS's background list and the
hosts of parts that are off (S07-egress-gateway, "Packet path",
DNS), so prover runs are bounded (SEC13-bounded-resources).
Requests are deduplicated by host, and each VM has limits on
pending and new requests (S09-09-approval-flow). A deny holds
until the sessions running at the deny have ended, and an answer
given after the VM's sessions changed is refused and the request
shown again as a new request (S09-09-approval-flow, S07-egress-gateway, "Deny duration", "Session set at
the answer"). Each VM has a cap on concurrent
prover runs (S04-architecture). Boundary results are cached per host,
port and rule form, keyed on the SHA-256 of the boundary document, so
a changed boundary is checked again. A request over a limit stays
denied and is logged with the rule that limited it.

### Failure

A request whose risk check or boundary check fails or
can't run can't be approved: the destination stays denied, and the
failure is logged.

### Join approval

When the risk check finds something in a joining
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
- *Expiry.* The set a join approval records holds the sessions in
  the VM and the waiting `wb` process. The request expires when that
  `wb` exits, or when the VM's set of sessions changes before the
  answer. The expiry is a Device Config State Change (5019) event
  with the rule `approval-expired`, and the start is refused.

### Delivery

Approval requests are delivered as native notifications (through the
platform's notification helper, S12-platforms) and through `wb approve` /
`wb deny`. They are never written to the terminal stream of an agent
session, which the guest controls. Guest-influenced text in a request is
stripped of control and escape sequences wherever it is shown.

**Status:** Draft
