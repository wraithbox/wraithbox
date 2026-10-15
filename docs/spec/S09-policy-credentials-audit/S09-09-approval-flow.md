# S09-09 - Approval Flow

**Purpose:** How an approval request for a name behaves, from the lookup that
raises it to its answer or its expiry.

## Design

How an approval request for a name behaves, from the lookup that
raises it to its answer or its expiry. The maintainer decided this on
I51 (B51-approval-flow, option A with its recommended sub-decisions).
The VM is the unit for each rule here, because the host can't tell
which session looked a name up (S07-egress-gateway, "Enforced per
VM", I36).

### What the blocked client sees

`wb-netd` holds the answer to a
refused lookup for 4 seconds (S07-egress-gateway, "Packet path",
DNS, "Hold"). When the user approves within the hold, the waiting
lookup gets the allowed answer, and the tool works on its first try.
Otherwise it gets `NXDOMAIN` with a negative TTL of 0 ("Negative
answers" there), and the tool fails. After a later approval, the
next lookup reaches `wb-netd` and succeeds, so the agent must retry.

### Kept out of notifications

A name on the guest OS's background
list, a host refused with `profile-part-off`, a name that fails the
name form rule, and a name collected in a learn period don't raise a
request (S07-egress-gateway, DNS and "Approvals and learning"). The
image turns off the services behind the background list where it
can (X17-image-build).

### One request per name

A VM has at most one open request per name.
Later lookups of that name join it and are counted on it. The
request shows the count in its body as guest-driven, "the guest
looked this up N times", never in the notification's title.

### Request ids

Each request has an id that `wb-hostd` never
reuses. The notification's actions and `wb approve <id>` and
`wb deny <id>` name it, and an answer applies only to the request
with that id.
- *Stale answers.* An answer whose id doesn't name an open request, such
  as one for a request that expired or was replaced, is refused and
  logged with the rule `approval-stale`. So an old answer can't apply
  to a newer request for the same name, which may have other
  sessions or other risk-check findings.
- *Replaced requests.* When an answer is refused because the VM's
  sessions changed (S07-egress-gateway, "Session set at the
  answer"), the request closes and its notification is withdrawn. A
  new request with a new id and the current set of sessions
  replaces it, and the risk check runs again against the VM's
  current union. The new request's 5019 event names the old id. The
  host caused the replacement, so it doesn't count against the rate
  of new requests.
- *Id required.* `wb approve` and `wb deny` need an id. Without one
  they are refused, and the error lists the pending ids (S05-cli).
  So the user answers the request they read, not one the guest
  swapped in after it.

### Limits

Each VM has two limits fixed in code: 16 pending
requests, and 10 new requests a minute.
- *Authoritative in `wb-hostd`.* `wb-hostd` keeps each VM's count of
  pending requests and its rate of new requests, and applies both
  limits to every approval event it receives. An event over either
  limit is dropped and logged
  with the rule `approval-pending-cap` or `approval-rate`. It
  doesn't raise a request, so the user sees no notification for it.
  So a compromised or restarted `wb-netd` can't raise more requests than
  the limits allow.
- *Mirrored in `wb-netd`.* `wb-netd` checks both limits when the
  query arrives, under the lock of the one-open-event-per-name
  check (S07-egress-gateway, "Same answer for every refusal"), only
  to decide the answer and the hold. `wb-hostd` tells it when a
  request closes, and a new `wb-netd` gets the VM's pending count
  and recent rate from `wb-hostd` at start, with the policy
  (S04-07-supervision, "Restarting a daemon"). A name over either
  limit is refused at DNS with the same answer and hold as any
  unknown name, without a request, and is logged with the same
  rule. These refusals are aggregated per rule
(S07-egress-gateway, "Events and rate limits"). `wb status` shows
the VM's pending requests and the count of names refused under each
limit since the VM started. The guest chooses which names it looks
up, so it could raise requests to wear down the user's attention
(T04-bad-approvals). These limits are the control against that,
and they bound the prover runs too (SEC13-bounded-resources).

### Expiry

A request has no wall-clock timeout. It stays pending, in
`wb status` and as a notification, until the user answers it or
until every session in the set it records has ended
(S07-egress-gateway, "Session set at the answer"). A replaced
request's set is the one it was replaced with, so a request
replaced with sessions A and B doesn't expire when A ends. A
request that expires has its notification withdrawn, and the expiry
is a Device Config
State Change (5019) event with the name, the sessions and the rule
`approval-expired`. An expired request grants nothing, and the next
lookup of the name raises a new request.

### Name display

A notification, `wb approve`, `wb deny` and
`wb status` show a name in ASCII, as `wb-netd` received it but in
lowercase and without a trailing dot. The name
form rule lets only letters, digits, hyphens, and dots through
(S07-egress-gateway, DNS, "Name form"), and the display code still
escapes every other byte as `\xNN`, as a second layer. An
internationalized name arrives in punycode, with labels that start
with `xn--`, and is shown in that form.
- *Unicode form.* `wb-hostd` decodes such a name with the IDNA 2008
  lookup rules and shows the Unicode form next to the ASCII form,
  marked as decoded. When a label fails to decode, no Unicode form
  is shown, and the request shows a warning that the name isn't a
  valid internationalized name.
- *Warning.* A warning next to the Unicode form names the reason
  when a label mixes scripts or the name holds a confusable
  character. The rule the user approves always holds the ASCII
  form.
  - *Mixed scripts:* a label holds letters from more than one
    script (UTS 39, Single Script). Characters of the Common and
    Inherited scripts, such as digits and hyphens, don't count.
  - *Confusable:* a non-ASCII character that Unicode's
    `confusables.txt` (UTS 39) maps to a prototype in another
    script, or to an ASCII letter or digit. ASCII characters never
    count, so `bücher1.de` doesn't warn.
- *Parser.* The decoder and the script check read bytes the guest
  chose, so they have a fuzz target (C03-policy-and-approvals).

### Allow for this session

Shown as "for this VM until these
sessions end", with the sessions listed (S07-egress-gateway, "Who an
approval reaches").

**Status:** Draft
