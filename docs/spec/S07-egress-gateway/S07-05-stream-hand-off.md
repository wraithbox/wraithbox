# S07-05 - Stream Hand-off

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
  (S07-06-name-mapping).
- What `wb-netd` writes with a stream can only narrow ("Only
  narrows").

### Transport

Each channel on this page is a Unix domain socketpair with no path, of
type `SOCK_SEQPACKET` or `SOCK_DGRAM`, as for the other descriptor
channels (S04-architecture). macOS has no `SOCK_SEQPACKET` for Unix
domain sockets, so it uses `SOCK_DGRAM`.

The design needs messages in the order they were sent, and a message
that doesn't fit to fail at the sender instead of being dropped.
On macOS the ordering test of C11-host-daemons ("Descriptor channels")
shows both on a channel of `fdchan.Socketpair(4096)`, and how many
records it queues before a send fails with `ENOBUFS`. A Linux or
Windows host gets the same test when its channels are built
(S12-platforms).

A send that fails, or that blocks for more than 100 ms, counts as
failed. A sender never retries out of order. `wb-netd` then refuses
the name of a report with the answer every refusal gets
(S07-04-dns-refusals), or resets a stream's TCP connection. Both are
logged with the rule `handoff-send-failed`.

A process that passes a socket keeps its copy until the receiver
holds it (S04-12-descriptor-passing). For a stream, the proof is
`wb-proxyd`'s first byte on it or a `StreamReset` for it. `wb-netd` keeps a
stream's copy no longer than a deadline (SEC13-bounded-resources),
then closes it without a reset, logged `stream-hold-expired`. Until
then it doesn't read end of file there. So a stream that `wb-proxyd`
closes before a byte or a `StreamReset`, or drops for a full
descriptor table, gives the guest its FIN at the deadline.

### The contract

The messages of the hand-off socket, the control channels and the
accounting channels are in the package `wraithbox.egress.v1`
(`proto/wraithbox/egress/v1`): `handoff.proto`, `control.proto` and
`accounting.proto`. Their comments state each field's bounds.
`internal/egressmsg` holds the form check of each direction, with a
fuzz target each (C02-streams-and-tls, "Fuzzing"). Each channel is made with
`fdchan.Socketpair(4096)`. Each end sends the version record first
(S04-architecture, "The version record").

A message doesn't name a VM when the socket it came on already does.
A hand-off message names none: its VM is the socket's. A `wb-netd`'s
control and accounting channels are its VM's. Only the messages
between `wb-hostd` and `wb-proxyd`, which serves every VM, name one.

### Message form

A message from `wb-netd` (`NetdMessage`) is one of three kinds:

| Kind | Descriptors |
|---|---|
| name report | none |
| release | none |
| stream | exactly one |

A hand-off message is at most 4 KiB, fixed in code. `wb-proxyd` reads
with a buffer one byte larger, and a control buffer with room for at
least two descriptors. On macOS the control buffer holds every
descriptor that one message can hold, with room to spare. Its kernel
puts each descriptor of a message in the receiving process even when
the control buffer cuts it off, and one cut off can't be closed. `wb-proxyd`
refuses a message that:

- is over the size;
- is truncated (`MSG_TRUNC`);
- has control data that was cut (`MSG_CTRUNC`);
- holds another number of descriptors than its kind allows;
- arrived while the descriptor table was full, so the kernel discarded
  its descriptors.

Control data that was cut also ends the channel, because the process
may then hold descriptors that it can't name.

It closes every descriptor that arrived with a refused message.

Then `egressmsg.DecodeNetdMessage` refuses a message that doesn't
decode, holds a field number its message doesn't have at any depth,
doesn't set a kind, or breaks a field's bound. A record with two kinds
on the wire decodes as the last one, as protobuf decodes a `oneof`, and
that kind's checks apply. A name must already be in canonical form
(S07-06-name-mapping, "Name reports"), and every address inside the
synthetic range of S07-03-dns. A refused message is malformed
(S07-06-name-mapping, "Malformed reports").

The names and labels in hand-off messages are guest strings. A log
record holds them escaped and cut (S07-02-packet-path, "Events and rate limits").

`wb-proxyd` decides the messages of one hand-off socket in the order
it read them. So a stream behind a report that waits on `wb-hostd`
(the wildcard budget, S07-06-name-mapping) waits too. It isn't refused
for a missing entry.

### Resets

`wb-proxyd` sends one message kind on the hand-off socket,
`StreamReset`, with the stream id that `wb-netd` gave the stream. It
sends it when it resets a stream it accepted, before it closes the
stream's socket. So `wb-netd` knows to reset the guest's TCP
connection, not to close it, when it reads end of file on that socket.

`wb-netd` reads what waits on the hand-off socket and sends every byte
it already queued for the guest. Then it answers end of file with a
FIN, or with a reset when a `StreamReset` for the stream came first
(`stream-reset`). Another read error gets a reset, logged
`proxyd-stream-read-failed` when no `StreamReset` came. A guest that
takes no bytes within a deadline (`guest-write-timeout`,
SEC13-bounded-resources) or fails a write (`guest-write-failed`) is
reset. Only the path that resets a stream first logs it.

When a `StreamReset` send fails (`ENOBUFS` or its deadline), `wb-proxyd`
closes the VM's hand-off socket before the stream's socket, and
`wb-netd` resets every stream of a VM whose hand-off socket closed
(`stream-reset-unsent`, S07-20-handoff-sockets, "Closing a socket").

### One hand-off socket per VM

`wb-hostd` creates a hand-off socketpair for a VM at each start and
restore, and at the restarts of S04-07-supervision, "Restarting a
daemon". It passes one end to `wb-proxyd`, and the other to the VM's
`wb-netd` once `wb-proxyd` acknowledges its end. It starts or resumes
the VM only once `wb-netd` acknowledges its end too.
S07-20-handoff-sockets has the socket's states, its number, how it
closes, and what each restart does to it.

The build VM gets no hand-off socket, and its `wb-netd` hands no
stream (S07-18-build-network).

### From `wb-hostd`

`wb-hostd` passes these ends over a socketpair it gives `wb-proxyd` at
spawn, its control channel (S04-architecture, "Control channels").
`wb-netd` never holds an end of it.

Each message (`HandoffSocket`) holds exactly one descriptor, and the
fields VM, generation and socket number. `wb-hostd` keeps each VM's
generation in `state.db`, and only increases it. S07-06-name-mapping
("Lifetime") has the `fresh` field.

`wb-proxyd` refuses the message when:

- the message form is wrong;
- the descriptor isn't a Unix domain socket of the platform's hand-off
  type;
- the VM isn't a VM slot of S06-04-vms-and-placement. The build VM of
  an image build has no hand-off socket (S07-18-build-network);
- the generation is older than the one it holds for the VM;
- the socket number isn't above the one it holds for the VM.

It then closes the message's descriptors and the control channel, as
`control.proto` says. It logs the refusal with the rule
`handoff-socket-refused` and the check that failed. A datagram whose
descriptors the channel refuses gets `control-message-refused`.

A message it accepts replaces the VM's old socket, and every stream
that came on it closes. With the generation it holds, that is the new
socket of a daemon restart (S04-07-supervision, "Restarting a daemon").
After a `wb-proxyd` restart, `wb-proxyd` holds no socket, and each
VM's first message sets its generation and number.

### Streams

A stream message holds exactly one descriptor, a Unix domain
`SOCK_STREAM` socket. Its fields are:

- the generation;
- the synthetic address and port;
- a stream id, which a `StreamReset` names ("Resets");
- once X14-flow-attribution delivers labels, the guest's label. Its
  field number is kept, and this build refuses a message that sets it.

A stream message of the wrong form is malformed (S07-06-name-mapping).
`wb-proxyd` refuses the stream when:

- the descriptor isn't a Unix domain `SOCK_STREAM` socket;
- the generation isn't the socket's;
- the copy has no entry for the address;
- the VM's current effective policy no longer allows the entry's
  name, or doesn't allow the port for it;
- a cap below is reached (`stream-cap`).

It then sends a `StreamReset` for the stream id and closes the
descriptor. It logs the refusal with the rule `stream-handoff-refused`
and the check that failed. A stream dropped for a full descriptor table
("Message form") gets no `StreamReset`, as its record is never read.
`wb-proxyd` resolves no name upstream and fetches no binding before
these checks pass.

### Caps

This section serves SEC13-bounded-resources. Per hand-off socket,
`wb-proxyd` caps the streams open from it and the hand-off messages it
has read but not yet decided on.

Each cap is configuration with a default. Each hand-off socket has its
own reader, so one VM's flood doesn't hold up another VM's streams.

### Only narrows

A field `wb-netd` writes either matches what `wb-proxyd` already
holds, or refuses the stream. The label can only narrow
(S07-01-per-vm-policy, "Narrowing by label").

So a compromised `wb-netd` can hand over a stream only under a name
and port that the VM's effective policy allows, within the wildcard
budget. It can't give a stream another VM's policy, credential
bindings or CA, or the grants of a project with no session in the VM.

Some checks only `wb-netd` makes: the link filter, DHCP and its per-VM
caps, the query rate, and the count of refused lookups in the wildcard
budget. It alone gives the local and quiet answers and the one answer
to every refusal, and sends approval events and learn lists.

A compromised `wb-netd` that skips them can refuse or slow its own
VM's traffic, or send approval events. `wb-hostd` applies the request
limits to every event it receives, whatever `wb-netd` checked. So
those events can't raise more requests than the limits allow
(S09-10-approval-flow).

None of them lets a stream reach a name or port the VM's effective
policy refuses.

### Policy order

Every change to the effective policy goes to `wb-proxyd` first. Such
a change is a session start or end, an approval, a policy file edit,
or `wb allow`.

1. `wb-hostd` sends the change to `wb-proxyd` (`PolicyChange`, its
   sequence rising per VM run, from 1 again after a fresh `HandoffSocket`).
2. `wb-proxyd` applies it and acknowledges it (`PolicyAck`).
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
