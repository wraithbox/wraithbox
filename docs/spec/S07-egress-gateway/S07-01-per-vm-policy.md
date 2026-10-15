# S07-01 - Enforced per VM

**Purpose:** Why the VM is the unit the gateway enforces, how the policies of
its projects combine, and what happens when they conflict or change.

## Design

A VM has one guest MAC address and one leased IPv4 address
(S07-02-packet-path, "Addressing"), and every project user in it sends from them. The host
can't tell which project user, session or program opened a
connection, so the VM is the unit it enforces. The maintainer decided
this on I36 (B36-flow-attribution). In this spec a *session* is any
session `wb-hostd` starts for a project, a debug shell (`wb shell`)
included.

### Effective policy

A VM's effective policy is the built-in
profiles, the global policy, and the network policy, credential
bindings and dependency-gate overrides of every project that has a
session in the VM, merged as a union (S09-11-precedence), plus the approvals held for the VM (S07-11-approvals-and-learning). `wb-hostd` recomputes it whenever a session starts or
ends. A project's allow rules and bindings leave the effective
policy when its last session in the VM ends, and its denies, limits
and modes leave under the rules of "Session end".

### Limits and modes

Where projects set a limit, such as the
wildcard budget, the minimum age or the vulnerability threshold, the
VM gets the strictest value among them. A limit that shrinks during
an active period applies to the count already reached, and a refusal
under it names the project the limit came from
(NFR06-explained-refusals). Where projects set a host to `enforce`
and `audit`, the VM gets `enforce`, and the join notice and the
audit event name the host and both projects. A host in pass mode for
one project and inspected for another is a conflict (below).

### Conflicts refuse the joining session

At each recompute
`wb-hostd` checks the union, and refuses the session start that
would create it, with an error that names both projects and the
host (NFR06-explained-refusals), when:
- a host that one project puts in pass mode has a credential binding
  from another project, or a built-in profile. These are the pass
  refusals of I40 (B40-learn-pass-modes), checked over the union and
  not only per file;
- a host that one project puts in pass mode is inspected under
  another project's policy;
- a host with a raw TCP entry from one project has a credential
  binding from another project, or a built-in profile. This holds
  for the host on any port (S07-10-non-http-streams, "Raw TCP entries");
- a host and port that one project names in a raw TCP entry expects
  another protocol under another project's policy, such as TLS on
  the same port. Raw TCP on one port and TLS on another port of the
  same host isn't a conflict (S07-10-non-http-streams, "One protocol per host and port");
- two projects bind the same host and port, with overlapping paths,
  to different secret store items;
- the union fails the extension check or the boundary check
  (S09-03-boundary-check);
- the risk check (S09-08-approvals) finds
  something in the joining project's rules and bindings compared
  with the current union, such as a binding on a host another project
  was approved to write to (`capability_expansion`), or an approved
  rule that gives another project's binding new reach
  (`credential_reach_expansion`). The start is refused, and the
  findings go to the user as a join approval
  (S09-08-approvals). Once the user
  approves it, the start can go ahead.

`wb-hostd` never settles a conflict by weakening a mode or picking
one binding. A check that fails or can't run refuses the start too
(fail closed).

### Policy changes while sessions run

A change *narrows* when the
new union allows a subset of what the old union allowed: no request,
host, method, path, credential injection or mode that the old union
refused becomes allowed. Removing an allow rule, a binding or an
approval, and tightening a limit or a mode, narrow, unless the
change moves a host and port to `audit`. Removing a `deny_rules`
entry, loosening a limit, and weakening a mode (pass over inspect,
`audit` over `enforce`) widen. A change that moves a host and port
from `enforce` to `audit` widens, whatever removal caused it, such
as removing the last `enforce` endpoint on it while an `audit`
endpoint stays (S09-11-precedence, "Mode per host and port"). Adding a raw TCP entry widens, and removing one narrows. A change that narrows
applies at once, without the union checks, because a subset of a
union inside the boundary is inside it too and doesn't add reach. If it
leaves a host in pass mode for one project and inspected for
another, the VM inspects it, and the 5019 event names the host and
both projects. A raw TCP stream can't be inspected. So if a change
that narrows leaves a host and port named in a raw TCP entry for one
project and expecting another protocol for another, the VM drops the
raw TCP entry and closes its relays, and the 5019 event names the
host, the port, and both projects. Open streams are then checked
again ("Open streams"). Every other change widens, including one that narrows in
part, and goes through the same checks as a joining session. If one
fails or can't run, the change is refused and logged with the
project and rule that caused it, the VM keeps its last effective
policy that passed, and `wb policy explain` shows the change as
pending. The user can still apply a removal as a change of its own.
A removal that moves a host and port to `audit` applies at once,
as other removals do, so revoking a rule never waits for the
prover. The host and port's `enforce` mode is kept as a held source
("Session end") while the checks run, with no rule from the removed
endpoint. The held source is dropped when the checks pass, and stays
when they fail or can't run. The 5019 event and `wb policy explain`
show it, and it stays until a recompute without it passes, or the
active period ends.
When a refused change narrows in part, the refusal and
`wb policy explain` name the narrowing part and say "apply the
removal on its own to revoke it now" (NFR06-explained-refusals).

### Session end

When a project's last session in the VM ends, its
allow rules and bindings, and the approvals whose session set has
fully ended, leave the union at once. That
part only narrows, unless it moves a host and port to `audit`
(below). The rest of what the project set can widen the
union when it leaves, and the recompute widens when any of these
leave with it:
- a `deny_rules` entry;
- a limit the project held at the strictest value;
- a part it turned off;
- `enforce` that any of its endpoints or approvals gave a host and
  port that another source puts in `audit`, which then resolves to
  `audit`;
- inspection it set on a host another project puts in pass mode,
  which the VM inspected (above);
- another protocol it set on a host and port that another project
  names in a raw TCP entry, which the VM dropped (above).

A recompute that widens goes through the extension check and the
boundary check of a joining session. The join risk check doesn't
run, because no project joins. If a check fails or can't run, the
VM keeps the departing project's `deny_rules`, limits, modes and
parts that are off as a held source. An `enforce` mode is held
alone: the host and port stays in `enforce`, and the allow rules
and approvals that gave it leave. A Device Config State Change
(5019) event names the project that left and the rule that failed,
and `wb policy explain` shows the held source and the widening as
pending. The held source stays until a recompute without it passes,
or the active period ends.

When a project joins while a held source exists, a held `enforce`
mode from a removal while sessions run ("Policy changes while
sessions run") included, `wb-hostd` runs the
extension check, the boundary check and the risk check on the union
without the held source, and drops the held source only if all
three pass. Otherwise the held source stays through the join, and
the risk check runs on the union with it. The join approval is
cached on the union without the held source
(S09-08-approvals, "Join approval").

### Open streams

After each recompute, `wb-proxyd` checks every
open stream of the VM against the new effective policy: a WebSocket,
an HTTP/2 connection, a pass relay, a raw TCP relay (S07-10-non-http-streams), a server-sent event stream, or a held download. A stream
whose allow rule, mode or binding is gone is closed and logged with the rule `policy-recomputed`. So a project's
bindings can't be reached while none of its sessions run.

### Join notice

At session start, `wb` prints the other projects
with a session in the VM, the credential bindings and write grants
of theirs that the new session can reach, and those of the new
project that they can reach. `wb status` lists the same reach for
each VM. Each recompute is a Device Config State Change (5019) event
that names the projects that joined or left (SEC10-audit).

### What is per project

The project's policy file and settings on
the host, its guest user and clone (S06-vm-lifecycle), and its
landing repository with the ref restriction per session
(S08-workspace-and-git). Pushes of returned work go over the
host-guest socket, not through this gateway.

### Narrowing by label

Per-project, per-session and per-program
rules can only narrow the effective policy, using a label the guest
reports for each flow (S13-guest-confinement). The host records the
label as reported by the guest, and guest root can forge it
(T06-forged-labels). Until X14-flow-attribution delivers labels,
nothing narrows the effective policy.

### Residual risk

Any process in a VM can use the grants of every
project with a session in it: their credential bindings, their
write grants under the git hosting profile, their dependency-gate
overrides and their approvals. The projects also share the VM's
limits (T11-shared-vm-grants). A project whose grants must stay
apart is set to `placement = isolated`, and `wb-hostd` then gives it the
isolated VM to itself (S06-04-vms-and-placement).

### Active period

The wildcard budget is counted per *active period*: from the first
session start in the VM to the end of the last session running in it.

**Status:** Draft
