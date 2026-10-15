# S09-10 - Approval Flow

**Purpose:** How an approval request for a name behaves, from the lookup that
raises it to its answer or its expiry.

## Design

The VM is the unit for each rule here. The host can't tell which
session looked a name up (S07-01-per-vm-policy, I36).

### What the blocked client sees

`wb-netd` holds the answer to a refused lookup for 4 seconds
(S07-04-dns-refusals, "Hold").

- When the user approves within the hold, the waiting lookup gets the
  allowed answer. The tool works on its first try.
- Otherwise it gets `NXDOMAIN` with a negative TTL of 0 (S07-03-dns, "Negative answers"), and the tool fails. After a later approval, the
  next lookup reaches `wb-netd` and succeeds. So the agent must retry.

### Kept out of notifications

These don't raise a request (S07-03-dns, and
S07-17-approvals-and-learning):

- a name on the guest OS's background list;
- a host refused with `profile-part-off`;
- a name that fails the name form rule;
- a name collected in a learn period.

The image turns off the services behind the background list where it
can (X17-image-build).

### One request per name

A VM has at most one open request per name. Later lookups of that name
join it, and are counted on it.

The request shows the count in its body, as guest-driven: "the guest
looked this up N times". It never shows it in the notification's
title.

### Request ids

Each request has an id that `wb-hostd` never reuses. The
notification's actions, `wb approve <id>` and `wb deny <id>` name it.
An answer applies only to the request with that id.

#### Stale answers

An answer whose id doesn't name an open request is refused, and logged
with the rule `approval-stale`. An example is an answer for a request
that expired or was replaced.

So an old answer can't apply to a newer request for the same name. The
newer one may have other sessions or other risk-check findings.

#### Replaced requests

An answer can be refused because the VM's sessions changed
(S07-04-dns-refusals, "Session set at the answer"). Then:

1. the request closes, and its notification is withdrawn;
2. a new request, with a new id and the current set of sessions,
   replaces it;
3. the risk check runs again, against the VM's current union.

The new request's 5019 event names the old id. The host caused the
replacement, so it doesn't count against the rate of new requests.

#### Id required

`wb approve` and `wb deny` need an id. Without one they are refused,
and the error lists the pending ids (S05-cli).

So the user answers the request they read, not one the guest swapped in
after it.

### Limits

Each VM has two limits, fixed in code:

- 16 pending requests;
- 10 new requests a minute.

The guest chooses which names it looks up. So it could raise requests
to wear down the user's attention (T04-bad-approvals). These limits are
the control against that. They bound the prover runs too
(SEC13-bounded-resources).

#### Authoritative in `wb-hostd`

`wb-hostd` keeps each VM's count of pending requests and its rate of
new requests. It applies both limits to every approval event it
receives.

An event over either limit is dropped, and logged with the rule
`approval-pending-cap` or `approval-rate`. It doesn't raise a request,
so the user sees no notification for it.

So a compromised or restarted `wb-netd` can't raise more requests than
the limits allow.

#### Mirrored in `wb-netd`

`wb-netd` checks both limits when the query arrives, only to decide the
answer and the hold. It checks them under the lock of the
one-open-event-per-name check (S07-04-dns-refusals, "Same answer for every refusal").

- `wb-hostd` tells it when a request closes.
- A new `wb-netd` gets the VM's pending count and recent rate from
  `wb-hostd` at start, with the policy (S04-07-supervision, "Restarting a daemon").

A name over either limit is refused at DNS, with the same answer and
hold as any unknown name, without a request. It is logged with the same
rule. These refusals are aggregated per rule (S07-02-packet-path, "Events and rate limits").

`wb status` shows the VM's pending requests, and the count of names
refused under each limit since the VM started.

### Expiry

A request has no wall-clock timeout. It stays pending, in `wb status`
and as a notification, until:

- the user answers it; or
- every session in the set it records has ended (S07-04-dns-refusals, "Session set at the answer").

A replaced request's set is the one it was replaced with. So a request
replaced with sessions A and B doesn't expire when A ends.

A request that expires has its notification withdrawn. The expiry is a
Device Config State Change (5019) event, with the name, the sessions
and the rule `approval-expired`.

An expired request grants nothing. The next lookup of the name raises a
new request.

### Name display

A notification, `wb approve`, `wb deny` and `wb status` show a name in
ASCII: as `wb-netd` received it, but in lowercase and without a
trailing dot.

The name form rule lets only letters, digits, hyphens and dots through
(S07-03-dns, "Name form"). The display code still escapes every other
byte as `\xNN`, as a second layer.

An internationalized name arrives in punycode, with labels that start
with `xn--`. It is shown in that form.

#### Unicode form

`wb-hostd` decodes such a name with the IDNA 2008 lookup rules. It
shows the Unicode form next to the ASCII form, marked as decoded.

When a label fails to decode, no Unicode form is shown. The request
shows a warning that the name isn't a valid internationalized name.

#### Warning

A warning next to the Unicode form names the reason when a label mixes
scripts, or the name holds a confusable character. The rule the user
approves always holds the ASCII form.

- *Mixed scripts:* a label holds letters from more than one script
  (UTS 39, Single Script). Characters of the Common and Inherited
  scripts, such as digits and hyphens, don't count.
- *Confusable:* a non-ASCII character that Unicode's `confusables.txt`
  (UTS 39) maps to a prototype in another script, or to an ASCII letter
  or digit. ASCII characters never count, so `bücher1.de` doesn't warn.

#### Parser

The decoder and the script check read bytes the guest chose. So they
have a fuzz target (C03-policy-and-approvals).

### Allow for this session

"Allow for this session" is shown as "for this VM until these sessions
end", with the sessions listed (S07-17-approvals-and-learning, "Who an approval reaches").

## Decisions

### The approval flow

How an approval request for a name behaves, from the lookup that raises
it to its answer or its expiry.

- **Decided:** I51 (B51-approval-flow, option A with its recommended
  sub-decisions).

**Status:** Draft
