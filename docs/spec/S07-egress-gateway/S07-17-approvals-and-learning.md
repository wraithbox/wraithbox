# S07-17 - Approvals and Learning

**Purpose:** How an unknown destination reaches the user as an
approval request, what an approval grants, and how learn mode collects
names.

## Design

### Approval requests

An unknown destination produces an approval event in `wb-hostd`, except
for these names (S07-04-dns-refusals):

- a name on the guest OS's background list;
- a host refused with `profile-part-off`.

The event is shown as a native notification, and listed by
`wb status`. The user picks one of:

- *allow for this session*;
- *allow for this project*;
- *deny*.

Each request shows the result of the risk check on the rule it would
add (S09-policy-credentials-audit). Examples are new reach for a
credential, or a new write method.

Policy changes take effect without restarting anything. An approval
that adds an inspected host doesn't need a new CA, because the CA has
no name constraints (S09-06-ca-keys).

### Who an approval reaches

The host can't tell which session looked up a name
(S07-01-per-vm-policy).

- A request names the VM and the sessions running in it.
- A project or session the guest reported is shown, marked as
  reported by the guest and untrusted.

The answers:

- *Allow for this session* is shown as "for this VM until these
  sessions end", with the sessions listed. It holds the rule for the
  VM until every session that was running when the user approved has
  ended.
- *Allow for this project* saves the rule to the policy of the project
  the user picks. The request preselects no project, also not from the
  guest's label. The rule then applies to the whole VM whenever that
  project has a session in it.
- *Deny* holds for the VM until every session that was running when
  the user denied has ended (S07-04-dns-refusals, "Deny duration").

Either way, any process in the VM can use an allowed rule
(T11-shared-vm-grants), and the request says so. The 5019 event of the
answer records the guest's label, if any, and the project the user
picked.

An answer given after the VM's sessions changed is refused. The
request is shown again as a new request, with a new id
(S07-04-dns-refusals, "Session set at the answer").

### What an approval grants

On a host without a built-in profile, an approval grants the read
methods GET, HEAD and OPTIONS.

A write method is refused until the user approves it in a separate
request (fail closed, SEC06-repo-writes). The risk check flags that
request as a new write method. A write method is any other method,
and a GET with `Upgrade: websocket`, which opens a channel both ways.

An approval is bound by the boundary (S09-09-approvals). A rule
outside it isn't shown as a request.

### Learn mode

Learn mode is only for projects with `placement = work`, not in an
`--isolated` session (FR10-learn-mode). It changes how unknown names
reach the user, not what the guest can reach. SEC05-default-deny holds
as written during a learn-mode session.

#### Refused as usual

A name that isn't allowlisted gets `NXDOMAIN`. The answer bytes, the
hold and the wildcard budget are the same as outside learn mode
(S07-03-dns, S07-04-dns-refusals).

The rules of allowed hosts all hold:

- HTTP policy;
- the git hosting profile (SEC06-repo-writes);
- the dependency gate (SEC07-dep-gate);
- credential replacement and placeholder binding.

Learn mode never adds a rule, a pass host or a raw TCP entry.

#### Collected

During a learn period in the VM ("The list"), an unknown name doesn't
raise an approval event or a notification. `wb-hostd` puts it on the
learn list, with:

- the time;
- the count of lookups;
- the guest's program label, if any, shown as reported by the guest
  and untrusted.

The refusal is logged with the rule `learn-collected` (SEC10-audit).

These are refused as outside learn mode, and don't go on the list:

- a name answered locally (`.arpa` and `.local`);
- a name refused for its form;
- a name on the guest OS's background list;
- a host refused with `profile-part-off`;
- a denied name.

#### The list

A learn list belongs to the learn project, and the set of sessions of
its learn period. The period runs from the start of the project's
first learn-mode session in the VM, to the end of the last learn-mode
session in the VM. Collection, the period and the refusal of other
projects ("One project at a time") all use this span.

- After the period ends, the list stays for review.
- A new learn period of the project starts a new list, which replaces
  the old one.
- The user can clear a list from `wb learn report`.

A list holds at most as many distinct names as a cap fixed in code
allows (SEC13-bounded-resources). Past it, a new name is refused as
usual. It isn't collected, and is logged with the rule
`learn-list-full`.

Names are stored only after they pass the name form check and its
length limit. Program labels are cut as guest strings in log records
are (S07-02-packet-path, "Events and rate limits").

#### Review

At session end, `wb` prints the list in the session summary: at most
100 names, then "and N more". It does so as for the link list
(S16-02-drops-and-links, "Links").

- The summary and `wb learn report` also say how many names were
  dropped past the cap.
- Both follow S16-03-around-the-stream, "Guest text elsewhere".
- The report shows each name with its risk-check findings and its
  boundary result (S09-09-approvals).

The user allows the names they accept with `wb allow`, or approves a
selection from the report in one batch. A name with any finding, or
whose rule would leave the boundary, is left out of the batch. The
user allows it on its own with `wb allow`.

#### What acceptance grants

An accepted name is saved to the learn project's network policy, as
*Allow for this project* does. The project is fixed by the list, not
picked by the user. It grants what an approval grants ("What an
approval grants").

- The review answers no live request. So the check on the session set
  at the answer doesn't apply.
- The new rule widens the policy. It goes through the checks of
  S07-01-per-vm-policy, "Policy changes while sessions run".
- Each accepted name is a Device Config State Change (5019) event,
  with its findings.

#### More than one run

The agent stops at the first refused host, and doesn't reach the hosts
behind it. So learning a project's hosts can take more than one run,
with the user allowing names between them.

#### One project at a time

The host can't attribute the destinations it collects to a project
(S07-01-per-vm-policy). So `wb-hostd`:

- starts a learn-mode session only when no session of another project
  runs in the VM;
- refuses a session of another project during the learn period. Each
  refusal names the other project (NFR06-explained-refusals).

Another session of the same project may start, with or without
`--learn`. The names it looks up during the learn period go on the
same list.

#### Leftover processes

When a project's last session ends, `wb-guestd` (S13-01-process-hardening):

1. locks that project user;
2. kills its processes until none remain;
3. boots out its per-user launchd domain.

So no process of another project is meant to run during a learn-mode
session. A process can still be left running, by guest root or by
launchd.

When `wb-guestd` reports one, `wb-hostd` refuses learn mode in that VM
until the VM is cold-booted (S06-10-leftover-processes). Its names
then reach the list, which the user reviews before any name is
allowed.

## Decisions

### Approvals grant reads only

An approval on a host without a built-in profile grants GET, HEAD and
OPTIONS. A write method needs a separate approval.

- **Decided:** I33 (B33-no-guest-credentials).

### Learn mode collects and refuses

Learn mode collects unknown names, and refuses them.

- **Why:** SEC05-default-deny then holds as written during a
  learn-mode session.
- **Decided:** I40 (B40-learn-pass-modes).

**Status:** Draft
