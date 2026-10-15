# S07-01 - Enforced per VM

**Purpose:** Why the VM is the unit the gateway enforces, how the
policies of its projects combine, and what happens when they conflict
or change.

## Design

A VM has one guest MAC address and one leased IPv4 address
(S07-02-packet-path, "Addressing"). Every project user in it sends
from them. In this spec a *session* is any session `wb-hostd` starts
for a project, a debug shell (`wb shell`) included.

### Effective policy

A VM's effective policy is the union of:

- the built-in profiles;
- the global policy;
- the network policy, credential bindings and dependency-gate
  overrides of every project that has a session in the VM, merged as
  S09-01-policy-files ("Precedence") says;
- the approvals held for the VM (S07-11-approvals-and-learning).

`wb-hostd` recomputes it whenever a session starts or ends. A
project's allow rules and bindings leave the effective policy when its
last session in the VM ends. Its denies, limits and modes leave under
the rules of "Session end".

### Limits and modes

Projects can set a limit, such as the wildcard budget, the minimum age
or the vulnerability threshold. The VM gets the strictest value among
them.

- A limit that shrinks during an active period applies to the count
  already reached.
- A refusal under a limit names the project the limit came from
  (NFR06-explained-refusals).

When one project sets a host to `enforce` and another to `audit`, the
VM gets `enforce`. The join notice and the audit event name the host
and both projects.

A host in pass mode for one project and inspected for another is a
conflict ("Conflicts refuse the joining session").

### Conflicts refuse the joining session

At each recompute `wb-hostd` checks the union. It refuses the session
start that would create one of these conflicts, with an error that
names both projects and the host (NFR06-explained-refusals):

- **Pass and a binding.** A host that one project puts in pass mode
  has a credential binding from another project, or a built-in
  profile. These are the pass refusals of I40 (B40-learn-pass-modes),
  checked over the union and not only per file.
- **Pass and inspect.** A host that one project puts in pass mode is
  inspected under another project's policy.
- **Raw TCP and a binding.** A host with a raw TCP entry from one
  project has a credential binding from another project, or a
  built-in profile. This holds for the host on any port
  (S07-10-non-http-streams, "Raw TCP entries").
- **Raw TCP and another protocol.** A host and port that one project
  names in a raw TCP entry expects another protocol under another
  project's policy, such as TLS on the same port. Raw TCP on one port
  and TLS on another port of the same host isn't a conflict
  (S07-10-non-http-streams, "One protocol per host and port").
- **Two bindings.** Two projects bind the same host and port, with
  overlapping paths, to different secret store items.
- **The boundary.** The union fails the extension check or the
  boundary check (S09-03-boundary-check).
- **The risk check.** The risk check (S09-08-approvals) finds
  something in the joining project's rules and bindings, compared with
  the current union. Examples are a binding on a host another project
  was approved to write to (`capability_expansion`), and an approved
  rule that gives another project's binding new reach
  (`credential_reach_expansion`). The findings go to the user as a
  join approval (S09-08-approvals). Once the user approves it, the
  start can go ahead.

`wb-hostd` never settles a conflict by weakening a mode or by picking
one binding. A check that fails or can't run refuses the start too
(fail closed).

### Policy changes while sessions run

A policy file can change while sessions of its project run.

#### Narrowing and widening

A change *narrows* when the new union allows a subset of what the old
union allowed. No request, host, method, path, credential injection or
mode that the old union refused becomes allowed.

These narrow, unless the change moves a host and port to `audit`:

- removing an allow rule, a binding or an approval;
- tightening a limit or a mode;
- removing a raw TCP entry.

These widen:

- removing a `deny_rules` entry;
- loosening a limit;
- weakening a mode: pass over inspect, `audit` over `enforce`;
- adding a raw TCP entry;
- moving a host and port from `enforce` to `audit`, whatever removal
  caused it. An example is removing the last `enforce` endpoint on it
  while an `audit` endpoint stays (S09-01-policy-files, "Mode per host
  and port").

Every other change widens, including one that narrows in part.

#### A change that narrows

A change that narrows applies at once, without the union checks. A
subset of a union inside the boundary is inside it too, and doesn't
add reach.

- If it leaves a host in pass mode for one project and inspected for
  another, the VM inspects it. The 5019 event names the host and both
  projects.
- A raw TCP stream can't be inspected. So if it leaves a host and port
  named in a raw TCP entry for one project and expecting another
  protocol for another, the VM drops the raw TCP entry and closes its
  relays. The 5019 event names the host, the port, and both projects.

Open streams are then checked again ("Open streams").

#### A change that widens

A change that widens goes through the same checks as a joining
session. If one fails or can't run:

- the change is refused, and logged with the project and rule that
  caused it;
- the VM keeps its last effective policy that passed;
- `wb policy explain` shows the change as pending.

The user can still apply a removal as a change of its own. When a
refused change narrows in part, the refusal and `wb policy explain`
name the narrowing part. They say "apply the removal on its own to
revoke it now" (NFR06-explained-refusals).

#### A removal that moves a host to `audit`

Such a removal applies at once, as other removals do, so revoking a
rule never waits for the prover.

1. The host and port's `enforce` mode is kept as a held source
   ("Session end") while the checks run. It holds no rule from the
   removed endpoint.
2. The held source is dropped when the checks pass. It stays when they
   fail or can't run.
3. The 5019 event and `wb policy explain` show it. It stays until a
   recompute without it passes, or the active period ends.

### Session end

When a project's last session in the VM ends, its allow rules and
bindings leave the union at once. So do the approvals whose session
set has fully ended.

That part only narrows, unless it moves a host and port to `audit`
(see the list below).

The rest of what the project set can widen the union when it leaves.
The recompute widens when any of these leave with it:

- a `deny_rules` entry;
- a limit the project held at the strictest value;
- a part it turned off;
- `enforce` that any of its endpoints or approvals gave a host and
  port that another source puts in `audit`, which then resolves to
  `audit`;
- inspection it set on a host another project puts in pass mode,
  which the VM inspected ("Policy changes while sessions run");
- another protocol it set on a host and port that another project
  names in a raw TCP entry, which the VM dropped ("Policy changes
  while sessions run").

#### Held source

A recompute that widens goes through the extension check and the
boundary check of a joining session. The join risk check doesn't run,
because no project joins.

If a check fails or can't run, the VM keeps a held source: the
departing project's `deny_rules`, limits, modes and parts that are
off.

- An `enforce` mode is held alone. The host and port stays in
  `enforce`, and the allow rules and approvals that gave it leave.
- A Device Config State Change (5019) event names the project that
  left and the rule that failed.
- `wb policy explain` shows the held source and the widening as
  pending.

The held source stays until a recompute without it passes, or the
active period ends.

#### A join while a source is held

A project can join while a held source exists, a held `enforce` mode
from a removal while sessions run ("Policy changes while sessions
run") included.

1. `wb-hostd` runs the extension check, the boundary check and the
   risk check on the union without the held source.
2. It drops the held source only if all three pass.
3. Otherwise the held source stays through the join, and the risk
   check runs on the union with it.

The join approval is cached on the union without the held source
(S09-08-approvals, "Join approval").

### Open streams

After each recompute, `wb-proxyd` checks every open stream of the VM
against the new effective policy. An open stream is a WebSocket, an
HTTP/2 connection, a pass relay, a raw TCP relay
(S07-10-non-http-streams), a server-sent event stream, or a held
download.

A stream whose allow rule, mode or binding is gone is closed. It is
logged with the rule `policy-recomputed`. So a project's bindings
can't be reached while none of its sessions run.

### Join notice

At session start, `wb` prints the other projects with a session in the
VM. It prints the credential bindings and write grants of theirs that
the new session can reach, and those of the new project that they can
reach.

`wb status` lists the same reach for each VM. Each recompute is a
Device Config State Change (5019) event that names the projects that
joined or left (SEC10-audit).

### What is per project

These stay per project: the project's policy file and settings on the
host, its guest user and clone (S06-vm-lifecycle), and its landing
repository, with the ref restriction per session
(S08-workspace-and-git).

Pushes of returned work go over the host-guest socket, not through
this gateway.

### Narrowing by label

Per-project, per-session and per-program rules can only narrow the
effective policy. They use a label the guest reports for each flow
(S13-guest-confinement).

- The host records the label as the guest reports it.
- Guest root can forge it (T06-forged-labels).

Until X14-flow-attribution delivers labels, nothing narrows the
effective policy.

### Residual risk

Any process in a VM can use the grants of every project with a
session in it: their credential bindings, their write grants under the
git hosting profile, their dependency-gate overrides, and their
approvals.

The projects also share the VM's limits (T11-shared-vm-grants). A
project whose grants must stay apart is set to `placement = isolated`.
`wb-hostd` then gives it the isolated VM to itself
(S06-04-vms-and-placement).

### Active period

The wildcard budget is counted per *active period*. That is from the
first session start in the VM to the end of the last session running
in it.

## Decisions

### The VM is the unit of enforcement

The gateway enforces policy per VM, not per project, session or
program.

- **Why:** the host can't tell which project user, session or program
  opened a connection.
- **Decided:** I36 (B36-flow-attribution).

### A conflict refuses, never settles

`wb-hostd` refuses the session start or the change. It doesn't weaken
a mode or pick one binding, and a check that can't run refuses too.

- **Why:** fail closed.

**Status:** Draft
