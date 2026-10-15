# S07-04 - DNS Refusals

**Purpose:** How `wb-netd` refuses a name: approval events, the hold
before each refusal, quiet refusals, and the answer that is the same
for every reason.

## Design

The answers of `wb-netd` to allowed names, and the name form, are on
S07-03-dns.

### Names not on the allowlist

Names not on the allowlist get `NXDOMAIN` and raise an approval event
(FR09-approve-unknown). During a learn period in the VM they go on the
learn list instead (S07-17-approvals-and-learning).

The answer is held first ("Hold"). So a user who approves in
time sees the lookup succeed.

A VM has at most one open approval event per name. Later queries for
that name, from any session in the VM, are counted on it. The host
can't tell which session looked a name up (S07-01-per-vm-policy).

Over the VM's limits on pending requests and on new requests, a name
is refused the same way, with the rule `approval-pending-cap` or
`approval-rate`. It doesn't raise an approval event
(S09-10-approval-flow).

### Hold

`wb-netd` sends the answer to a refused query 4 seconds after the
query arrived, not before. That holds whatever the reason in the list
under "Same answer for every refusal".

Every step that depends on the reason runs when the query arrives:

- the DNS checks of S07-03-dns and this page, in their order;
- the wildcard budget debit, with its accounting record;
- the one-open-event-per-name check;
- the request limits;
- building the answer's bytes.

The end of the hold is a timer that only sends those bytes.

#### Widening during the hold

A change that widens the VM's effective policy, such as an approval,
runs each held query through every DNS check again (S07-03-dns and
this page). It runs
them as if they had just arrived, the audit budgets and the event
channel included.

#### What the re-run changes

The re-run reads policy and host state: the allowlist, denies, the
budgets, and the event channel.

- Each re-run query counts against the query rate limit. A burst of
  re-runs over the limit is refused with `dns-query-rate`, and those
  queries keep their timers.
- A debit already made for the query isn't made again.
- The re-run doesn't add to an open event's lookup count, doesn't
  raise a request, and doesn't add a learn list entry.

#### Result of the re-run

- An allowed result is sent at once. Its accounting record and name
  report go first, as for every allowed answer.
- A refusal keeps its refusal bytes and its timer. So a refusal always
  arrives 4 seconds after its query.
- A name approved while the VM is over its audit budget is still
  refused.

#### Deny or no answer

A deny, or no answer within the 4 seconds, gets the refusal at the end
of the hold.

### Hold limits

Each VM's `wb-netd` holds at most 1,024 queries at once by default.
That is a per-VM cap that is configuration like the others
(S07-02-packet-path, "Per-VM caps"). Every query of a name counts on
its own.

A held query costs a few hundred bytes. The default table then takes
well under a megabyte per VM.

#### Over the cap

A refused query over the cap is dropped without an answer.

- Its wildcard budget debit and accounting record are made first, as
  for every refused query. Accounting records come before any drop
  decision (S04-08-records-and-bounds, "Accounting and event channels").
- It doesn't raise or join an approval event, and doesn't go on a
  learn list.
- It is counted with the rule `dns-hold-full`, in runs per rule
  (S07-02-packet-path, "Events and rate limits").

Over UDP the guest's resolver sends it again after its own timeout.
Over TCP `wb-netd` closes the DNS-over-TCP connection that sent it, so
the client doesn't wait on an open connection.

#### TCP

`wb-netd` keeps a DNS-over-TCP connection open while it has a held
query. It counts the connection in the per-VM cap on DNS-over-TCP
connections until its last held answer is sent.

An over-cap query closes the connection anyway. The connection's held
queries leave the table at the close, and their timers don't fire.

### Deny duration

A deny holds for the VM until every session that was running when the
user denied has ended. Until then:

- the name gets `NXDOMAIN` without a new approval event;
- later queries are counted on the closed event.

After that, a lookup raises a new event.

### Session set at the answer

An approval event records the sessions running in the VM when it was
raised. It has a request id (S09-10-approval-flow).

If that set has changed when the user answers, the answer is refused
with the rule `approval-sessions-changed`. The request closes. A new
request, with a new id and the current sessions, replaces it.

### Quiet refusals

The guest OS resolves names of its own, with no user action. An idle
guest at the login window looked up 14 host names, 109 queries in all,
in its first hour (X03-network-path).

Names on the guest OS's background list get `NXDOMAIN`, and an audit
entry with the rule `guest-os-background`. They don't raise an
approval event.

The list is host-held:

- It ships with Wraith Box for each guest OS release.
- `wb-hostd` picks the list from the image it booted, never from
  anything the guest reports.
- Nothing the guest sends adds a name to it.

The list holds exact names, such as `init.push.apple.com`, never
suffixes or wildcards. So a lookup of `developer.apple.com` still
raises an approval event.

- A query matches when its name equals a listed name, compared
  case-insensitively, after one trailing dot is removed.
- A name on the list that is also allowed by policy is answered as
  allowed.
- `wb status` shows the list, and the count of refusals per name.

The image turns off the services that send these lookups where it can
(X17-image-build). So the list holds what remains.

### Parts that are off

A host can have all its built-in rules in profile parts that are off
(S07-11-http-policy, "Profile parts"). It gets `NXDOMAIN` and an audit
entry with the rule `profile-part-off`, whatever other rule allows it.
It doesn't raise an approval event. So an approval can't undo the
user's switch.

### Same answer for every refusal

The answer's bytes are the same whatever the reason a name is
refused, and so is the time of sending ("Hold"). The reasons are:

- name form;
- not allowlisted;
- denied;
- quiet list;
- part off;
- `learn-collected` and `learn-list-full`;
- `approval-pending-cap` and `approval-rate`;
- `wildcard-budget`, when the budget is used up;
- `dns-query-rate`;
- `accounting-write-failed`;
- `handoff-send-failed` (S07-05-stream-hand-off);
- `audit-budget`, `audit-bytes` and `event-channel-blocked`.

Each of them can be held. A held query costs only an entry in the
held-query table, whose cap drops what doesn't fit ("Hold limits").

The last three come from the host's own state, not from the name
(S04-08-records-and-bounds, "Accounting and event channels",
S09-11-audit). They don't raise an approval event or debit the
wildcard budget.

#### Order at arrival

When the query arrives, `wb-netd`:

1. checks the query rate (`dns-query-rate`) before anything else that
   can debit;
2. debits the wildcard budget;
3. writes the debit's accounting record to `wb-hostd`;
4. takes the one-open-event-per-name check and the checks of the
   request limits (S09-10-approval-flow) under one lock.

It does no other work that depends on the reason.

When the write fails, the name is refused with the rule
`accounting-write-failed` (S04-08-records-and-bounds, "Accounting and event channels"). An answered name's accounting record, with its
address, is written before its answer too. A failed write refuses the
name the same way.

#### Work beside the answer

The emission of the approval event and the notification start with
the hold, so the user can answer while the query waits. They, the
entry on a learn list and the audit entry run apart from the answer.
Nothing they do changes when the answer is sent ("Hold").

## Decisions

### Approval events per VM

One open approval event per name per VM, with queries from every
session counted on it.

- **Why:** the host can't tell which session looked a name up.
- **Decided:** I36 (B36-flow-attribution).

### Hold every refusal for 4 seconds

I51 decided to hold the answer for up to 4 seconds while a request is
open. The hold applies to every reason, not only to names with an open
request.

- **Why 4 seconds:** the macOS resolver library (`libresolv`,
  `RES_TIMEOUT`) waits 5 seconds before it sends a query again. The
  guest gets its answer before that retry. The guest's system
  resolver is `mDNSResponder`, and B51-approval-flow assumes it waits
  at least as long. C01-network-and-dns checks it.
- **Why every reason:** a shorter wait for the other reasons would let
  the guest tell them apart by the timing of the refusal, which "Same
  answer for every refusal" rules out.
- **Decided:** I51 (B51-approval-flow).

### A quiet list for the guest OS

Names the guest OS looks up by itself are refused without an approval
event, as an exception to FR09-approve-unknown.

- **Why:** an idle guest looks up names with no user action
  ("Quiet refusals").
- **Decided:** I17 (B17-network-path).

**Status:** Draft
