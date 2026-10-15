# S07-04 - Stream Hand-off

**Purpose:** How `wb-netd` hands each stream and each name to `wb-proxyd`, and the
checks that keep a compromised `wb-netd` from widening what a stream
may reach.

## Design

### Stream hand-off

(SEC04-no-guest-secrets, SEC08-proj-isolation).
`wb-netd` parses raw guest frames, which makes it the process most
likely to be compromised (S04-architecture). `wb-proxyd` takes nothing from
it that widens what a stream may reach. The maintainer decided on
I68 that a stream's VM comes from a per-VM hand-off socket that
`wb-hostd` creates and labels, that `wb-proxyd` re-checks the name
against its own copy of the name mapping, and that what `wb-netd`
writes with a stream can only narrow. The copy is built from checked
name reports (below, B68-proxyd-stream-identity). A name written
with each stream and checked only against policy was rejected,
because a `wb-netd` bug that mixes up two allowed names would then
go through. Moving the synthetic pool into `wb-proxyd` was rejected,
because it puts a round trip on every DNS answer and doesn't protect
more: a compromised `wb-netd` can still ask for any name the policy
allows.

### Transport

Each channel in this section is a Unix domain
socketpair with no path, of type `SOCK_SEQPACKET` or `SOCK_DGRAM`,
as for the other descriptor channels (S04-architecture). macOS
has no `SOCK_SEQPACKET` for Unix domain sockets, so it uses
`SOCK_DGRAM`. The design needs two properties of the type: messages
arrive in the order they were sent, and a message that doesn't fit
fails at the sender with an error and is never dropped silently.
I172 measures them on each host platform. A send that fails, or
that blocks for more than 100 ms, counts as failed, and a sender
never retries out of order. When a name report fails, `wb-netd`
refuses the name with the same answer as every refusal (S07-03-dns), and when a stream fails it resets the TCP connection.
Both are logged with the rule `handoff-send-failed`.

### Message form

A message from `wb-netd` is one of three kinds.
A name report and a release hold no descriptor, and a stream holds
exactly one. A hand-off message is at most 4 KiB, fixed in
code. `wb-proxyd` reads with a buffer one byte larger and a control
buffer with room for two descriptors. It refuses a message that is
over the size, truncated (`MSG_TRUNC`), or whose control data was
cut (`MSG_CTRUNC`), or that holds another number of descriptors
than its kind allows, and closes every descriptor that arrived
with it. The names and labels in hand-off messages are guest
strings, and a log record holds them escaped and cut (S07-02-packet-path, "Events and rate limits"). `wb-proxyd` decides the messages of one hand-off
socket in the order it read them. So a stream behind a report that
waits on `wb-hostd` (the wildcard budget, below) waits too, and
isn't refused for a missing entry.

### One hand-off socket per VM

`wb-hostd` creates a hand-off
socketpair for a VM at each start and restore, and at the restarts
of S04-07-supervision, "Restarting a daemon". It passes one end to
`wb-proxyd` and waits for `wb-proxyd` to acknowledge it. Only then
does it give the other end to `wb-netd`, as that section says:
at start to a new `wb-netd`, and on its control channel to a
running one. It starts or resumes the VM only after `wb-netd` holds its
end. `wb-proxyd` holds its end under the VM and generation
`wb-hostd` named. A newer end for the VM replaces the old one, and
`wb-proxyd` closes the old socket and every stream that came on it.

### From `wb-hostd`

`wb-hostd` passes these ends over a socketpair
it gives `wb-proxyd` at spawn. `wb-netd` never holds an end of it.
Each message holds exactly one descriptor and the fields VM and
generation. `wb-hostd` keeps each VM's generation in `state.db` and
only increases it. `wb-proxyd` refuses the message, closes its
descriptors, and logs the refusal with the rule
`handoff-socket-refused` and the check that failed when the message
form is wrong, the descriptor isn't a Unix domain socket of the
platform's hand-off type, the VM isn't a VM slot of
S06-04-vms-and-placement or the build VM of a running image build,
or the generation is older than the one it holds for the VM. A
message with the generation it holds is a replacement, the new
socket for the same VM and generation that `wb-hostd` sends at a
`wb-netd` restart (S04-07-supervision, "Restarting a daemon"). It
replaces the old socket as above. After a `wb-proxyd` restart,
`wb-proxyd` holds no socket, and each VM's first message sets its
generation.

### Replay

On each new hand-off socket, `wb-netd` first sends a
name report for every live entry of its mapping, in order and
before any other message. A replay send that fails with
`ENOBUFS` is retried in order, so no later message overtakes it.
`wb-proxyd` checks each report as below, with the bounds that
`wb-hostd` puts on mapping entries: at most one entry per address
of the pool, the name in canonical form and the address in the
synthetic range. After a `wb-proxyd` restart the replay rebuilds
its copy. After a `wb-netd` restart the new `wb-netd` holds the
mapping that `wb-hostd` kept from its accounting records
(S04-08-records-and-bounds, "Accounting and event channels", and
S04-07-supervision, "Restarting a daemon"), and its replay matches the copy `wb-proxyd` kept.

### Name reports

For each `A` answer with a synthetic address,
`wb-netd` sends the name and the address on the VM's hand-off
socket before the answer leaves. With the ordering property above,
the report arrives before any stream to that address. A report
doesn't hold a descriptor. `wb-proxyd` adds the pair to its copy of the
VM's mapping only when all of these hold:
- the name is in canonical form, lowercase and without a trailing
  dot, and passes the name form rule (S07-03-dns);
- the VM's effective policy allows the name;
- the address is in the synthetic range;
- the copy doesn't hold the address for another name;
- a name allowed only by a wildcard entry fits the wildcard budget
  (below).

Otherwise it drops the report and logs it with the rule
`name-report-refused` and the check that failed: `message-form`,
`name-form`, `not-allowed`, `synthetic-range`, `address-conflict`
or `wildcard-budget`. A report or release that fails the message
form, the name form or the synthetic range is malformed, and an
honest `wb-netd` never sends one, because `wb-hostd` checks the
same entry first. `wb-proxyd` then also closes the VM's hand-off
socket and every stream that came on it, and tells `wb-hostd` on
its control channel. `wb-hostd` ends that `wb-netd` and counts the
exit toward the restart limit, as for a malformed accounting record
(S04-08-records-and-bounds, "Accounting and event channels"). A report whose address the copy holds for
another name means `wb-netd` gave out an address it hadn't
released. `wb-proxyd` then removes the old entry too and closes
every open stream to that address. Streams to it are then refused
until a later report is accepted. A report that repeats an entry
the copy holds, such as a replay, doesn't change the copy.

### Releases

An entry stays in the copy until `wb-netd` releases its
address. `wb-netd` sends the release when it frees the address for
another name, after the TTL plus the guard interval and with no
connection to it open (S07-03-dns, "Synthetic pool"). So
`wb-netd`'s clock governs, and `wb-proxyd` doesn't expire entries
on a clock of its own. A stream already open keeps the name it got at
hand-off. A release for an address the copy doesn't hold is
logged and ignored. If a release can't be sent, `wb-netd` keeps the
address out of the pool and sends the release again, in order,
before any later report. The copy holds at most the 131,072 addresses of
the pool (SEC13-bounded-resources).

### Lifetime

The copy belongs to the VM. `wb-proxyd` keeps it
across a save and restore, as `wb-netd`'s mapping is
(S04-architecture), and time while the VM is saved counts as it
does for that mapping. The copy and `wb-netd`'s mapping are both
cleared only at a start that isn't a restore. Otherwise releases
alone remove entries, so a VM that idles and is saved and restored
keeps the answers its guest cached.

### Wildcard budget, second layer

`wb-netd` debits the wildcard
budget before it answers (S07-03-dns). `wb-proxyd` keeps a
second count against the same budget: the distinct names it
accepted only under a wildcard entry, per VM and active period. A
replayed or repeated name isn't counted again. Before it accepts a
name that adds to the count, `wb-proxyd` writes the name to
`wb-hostd` as an accounting record (S04-08-records-and-bounds, "Accounting and event channels"). A write that fails, or that blocks for more
than 100 ms, counts as failed, and the report is refused with the
rule `name-report-refused` and the check `wildcard-budget`. `wb-hostd` keeps the set per VM and active period and gives
it to a new `wb-proxyd` at start. A `wb-proxyd` restart thus keeps
the count. Refused lookups never reach `wb-proxyd`, so
its count is at most `wb-netd`'s.

### Streams

A stream message holds exactly one descriptor, a Unix
domain `SOCK_STREAM` socket, and the fields generation, synthetic address,
port and, once X14-flow-attribution delivers labels, the guest's
label. `wb-proxyd` refuses the stream, closes the descriptor, and
logs the refusal with the rule `stream-handoff-refused` and the
check that failed when the message form is wrong, the descriptor
isn't a Unix domain `SOCK_STREAM` socket, the generation isn't the socket's, the copy has no entry
for the address, the VM's current effective policy no longer allows
the entry's name or doesn't allow the port for it, or a cap below
is reached (`stream-cap`). It neither resolves the name upstream
nor fetches a binding before these checks pass.

### Caps

(SEC13-bounded-resources). Per hand-off socket, `wb-proxyd`
caps the streams open from it and the hand-off messages it has read
but not yet decided on. Each cap is configuration with a default.
It reads the hand-off sockets of all VMs in turn, so one VM's flood
doesn't hold up another VM's streams.

### Only narrows

A field `wb-netd` writes either matches what
`wb-proxyd` already holds or refuses the stream, and the label can
only narrow (S07-01-per-vm-policy, "Narrowing by label"). So a
compromised `wb-netd` can hand over a stream only under a name and
port that the VM's effective policy allows, within the wildcard
budget. It can't give a stream another VM's policy, credential
bindings or CA, or the grants of a project with no session in the
VM. The checks that only `wb-netd` makes are the link filter, DHCP
and its per-VM caps, the query rate, the count of refused lookups
in the wildcard budget, the local and quiet answers, approval
events and learn lists, and the rule that every refusal gets the
same answer. A compromised `wb-netd` that skips them can refuse or
slow its own VM's traffic, or send approval events. `wb-hostd`
applies the request limits to every event it receives, whatever
`wb-netd` checked, so those events can't raise more requests than
the limits allow (S09-09-approval-flow).
None of them lets a stream reach a name or
port the VM's effective policy refuses.

### Policy order

Every change to the effective policy (a session
start or end, an approval, a policy file edit, `wb allow`) goes to
`wb-proxyd` first. `wb-hostd` sends it to `wb-netd` only after
`wb-proxyd` has applied it and acknowledged it. So a report for a
newly allowed name isn't refused, and a narrowing applies in
`wb-proxyd` no later than in `wb-netd`. I173 measures the delay
this adds. When the acknowledgment doesn't come, because
`wb-proxyd` is waiting to be restarted or `wb-hostd` gave up on it,
`wb-netd` doesn't get a change that widens. A change that narrows
goes to `wb-netd` anyway. A restarted `wb-proxyd` starts from the
current effective policy.

**Status:** Draft
