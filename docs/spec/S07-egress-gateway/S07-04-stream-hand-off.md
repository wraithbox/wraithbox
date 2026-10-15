# S07-04 - Stream Hand-off

**Purpose:** How `wb-netd` hands each stream to `wb-proxyd`, and the
checks that keep a compromised `wb-netd` from widening what a stream
may reach.

## Design

### Stream hand-off

This page serves SEC04-no-guest-secrets and SEC08-proj-isolation.

`wb-netd` parses raw guest frames. That makes it the process most
likely to be compromised (S04-architecture). `wb-proxyd` takes nothing
from it that widens what a stream may reach:

- A stream's VM comes from a per-VM hand-off socket that `wb-hostd`
  creates and labels ("One hand-off socket per VM").
- `wb-proxyd` re-checks the name against its own copy of the name
  mapping. The copy is built from checked name reports
  (S07-13-name-mapping).
- What `wb-netd` writes with a stream can only narrow ("Only
  narrows").

### Transport

Each channel on this page is a Unix domain socketpair with no path, of
type `SOCK_SEQPACKET` or `SOCK_DGRAM`, as for the other descriptor
channels (S04-architecture). macOS has no `SOCK_SEQPACKET` for Unix
domain sockets, so it uses `SOCK_DGRAM`.

The design needs two properties of the type:

- messages arrive in the order they were sent;
- a message that doesn't fit fails at the sender with an error, and is
  never dropped silently.

I172 measures them on each host platform.

A send that fails, or that blocks for more than 100 ms, counts as
failed. A sender never retries out of order.

- When a name report fails, `wb-netd` refuses the name with the same
  answer as every refusal (S07-12-dns-refusals).
- When a stream fails, `wb-netd` resets the TCP connection.

Both are logged with the rule `handoff-send-failed`.

### Message form

A message from `wb-netd` is one of three kinds:

| Kind | Descriptors |
|---|---|
| name report | none |
| release | none |
| stream | exactly one |

A hand-off message is at most 4 KiB, fixed in code. `wb-proxyd` reads
with a buffer one byte larger, and a control buffer with room for two
descriptors. It refuses a message that:

- is over the size;
- is truncated (`MSG_TRUNC`);
- has control data that was cut (`MSG_CTRUNC`);
- holds another number of descriptors than its kind allows.

It closes every descriptor that arrived with a refused message.

The names and labels in hand-off messages are guest strings. A log
record holds them escaped and cut (S07-02-packet-path, "Events and
rate limits").

`wb-proxyd` decides the messages of one hand-off socket in the order
it read them. So a stream behind a report that waits on `wb-hostd`
(the wildcard budget, S07-13-name-mapping) waits too. It isn't refused
for a missing entry.

### One hand-off socket per VM

`wb-hostd` creates a hand-off socketpair for a VM at each start and
restore, and at the restarts of S04-07-supervision, "Restarting a
daemon".

1. It passes one end to `wb-proxyd`.
2. It waits for `wb-proxyd` to acknowledge it.
3. Only then does it give the other end to `wb-netd`, as that section
   says: at start to a new `wb-netd`, and on its control channel to a
   running one.
4. It starts or resumes the VM only after `wb-netd` holds its end.

`wb-proxyd` holds its end under the VM and generation `wb-hostd`
named. A newer end for the VM replaces the old one. `wb-proxyd` then
closes the old socket, and every stream that came on it.

### From `wb-hostd`

`wb-hostd` passes these ends over a socketpair it gives `wb-proxyd` at
spawn. `wb-netd` never holds an end of it.

Each message holds exactly one descriptor, and the fields VM and
generation. `wb-hostd` keeps each VM's generation in `state.db`, and
only increases it.

`wb-proxyd` refuses the message when:

- the message form is wrong;
- the descriptor isn't a Unix domain socket of the platform's hand-off
  type;
- the VM isn't a VM slot of S06-04-vms-and-placement, or the build VM
  of a running image build;
- the generation is older than the one it holds for the VM.

It then closes the message's descriptors. It logs the refusal with the
rule `handoff-socket-refused` and the check that failed.

A message with the generation it holds is a replacement. That is the
new socket for the same VM and generation that `wb-hostd` sends at a
`wb-netd` restart (S04-07-supervision, "Restarting a daemon"). It
replaces the old socket as above.

After a `wb-proxyd` restart, `wb-proxyd` holds no socket. Each VM's
first message sets its generation.

### Streams

A stream message holds exactly one descriptor, a Unix domain
`SOCK_STREAM` socket. Its fields are:

- the generation;
- the synthetic address and port;
- once X14-flow-attribution delivers labels, the guest's label.

`wb-proxyd` refuses the stream when:

- the message form is wrong;
- the descriptor isn't a Unix domain `SOCK_STREAM` socket;
- the generation isn't the socket's;
- the copy has no entry for the address;
- the VM's current effective policy no longer allows the entry's
  name, or doesn't allow the port for it;
- a cap below is reached (`stream-cap`).

It then closes the descriptor. It logs the refusal with the rule
`stream-handoff-refused` and the check that failed.

It neither resolves the name upstream nor fetches a binding before
these checks pass.

### Caps

This section serves SEC13-bounded-resources. Per hand-off socket,
`wb-proxyd` caps:

- the streams open from it;
- the hand-off messages it has read but not yet decided on.

Each cap is configuration with a default.

`wb-proxyd` reads the hand-off sockets of all VMs in turn. So one VM's
flood doesn't hold up another VM's streams.

### Only narrows

A field `wb-netd` writes either matches what `wb-proxyd` already
holds, or refuses the stream. The label can only narrow
(S07-01-per-vm-policy, "Narrowing by label").

So a compromised `wb-netd` can hand over a stream only under a name
and port that the VM's effective policy allows, within the wildcard
budget. It can't give a stream:

- another VM's policy, credential bindings or CA;
- the grants of a project with no session in the VM.

Some checks only `wb-netd` makes:

- the link filter, DHCP and its per-VM caps;
- the query rate;
- the count of refused lookups in the wildcard budget;
- the local and quiet answers;
- approval events and learn lists;
- the rule that every refusal gets the same answer.

A compromised `wb-netd` that skips them can refuse or slow its own
VM's traffic, or send approval events. `wb-hostd` applies the request
limits to every event it receives, whatever `wb-netd` checked. So
those events can't raise more requests than the limits allow
(S09-09-approval-flow).

None of them lets a stream reach a name or port the VM's effective
policy refuses.

### Policy order

Every change to the effective policy goes to `wb-proxyd` first. Such
a change is a session start or end, an approval, a policy file edit,
or `wb allow`.

1. `wb-hostd` sends the change to `wb-proxyd`.
2. `wb-proxyd` applies it and acknowledges it.
3. Only then does `wb-hostd` send it to `wb-netd`.

So a report for a newly allowed name isn't refused. A narrowing
applies in `wb-proxyd` no later than in `wb-netd`. I173 measures the
delay this adds.

The acknowledgment may not come, because `wb-proxyd` is waiting to be
restarted, or `wb-hostd` gave up on it. Then:

- `wb-netd` doesn't get a change that widens;
- a change that narrows goes to `wb-netd` anyway.

A restarted `wb-proxyd` starts from the current effective policy.

## Decisions

### Identity from `wb-hostd`, names re-checked, fields only narrow

A stream's VM comes from a per-VM hand-off socket that `wb-hostd`
creates and labels. `wb-proxyd` re-checks the name against its own
copy of the name mapping. What `wb-netd` writes with a stream can only
narrow.

- **Why:** `wb-netd` is the process most likely to be compromised.
- **Rejected:**
  - A name written with each stream and checked only against policy. A
    `wb-netd` bug that mixes up two allowed names would then go
    through.
  - Moving the synthetic pool into `wb-proxyd`. It puts a round trip
    on every DNS answer, and doesn't protect more: a compromised
    `wb-netd` can still ask for any name the policy allows.
- **Decided:** I68 (B68-proxyd-stream-identity).

**Status:** Draft
