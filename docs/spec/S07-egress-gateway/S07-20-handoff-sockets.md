# S07-20 - Hand-off Sockets

**Purpose:** The life of one VM's hand-off socket: its identity, the
acknowledgments of both ends, how it closes, who a failure is charged
to, what each restart does to it, and which of its decisions reach the
audit log.

**Requirements:** SEC08-proj-isolation, SEC10-audit,
SEC13-bounded-resources

**Tests:** C02-streams-and-tls, C11-host-daemons

Brief: B754-handoff-contract

![wb-hostd makes each hand-off socket, passes one end to wb-proxyd and, after its acknowledgment, the other to the VM's wb-netd. Each end acknowledges with the socket's number. wb-proxyd reports a socket it closes, and acknowledges the end of a VM's sockets at its stop.](S07-20-handoff-sockets.svg)

## Design

### Identity

A hand-off socket carries a VM's streams and name reports from its
`wb-netd` to `wb-proxyd` (S07-05-stream-hand-off). These values name it:

- the VM slot;
- the VM generation, which a start or restore raises
  (S04-02-vm-provider, "Restore");
- the socket number, which `wb-hostd` gives each new socket of the VM.
  It is at least 1, rises with each socket, and isn't reused while
  `wb-hostd` runs.

Every message about a socket names all three, except on `wb-netd`'s
control channel, whose VM is its own. Waits and state are keyed by
all three. A replacement in the same generation is a new socket, so
nothing meant for the old one reaches it.

### Messages

These are messages of `proto/wraithbox/egress/v1/control.proto`. The
last column says which ones I728 adds, with the socket number in each.

| Message | From | Means | Added by |
|---|---|---|---|
| `HandoffSocket` | `wb-hostd` | here is an end, with its VM, generation and number | the number |
| `HandoffSocketAck` | `wb-proxyd` | it holds the end it names | the number |
| `HandoffSocketHeld` | `wb-netd` | it holds the end it names | I728 |
| `HandoffSocketClosed` | `wb-proxyd` | it closed the socket it names, with a rule and a check | the number and two rules |
| `HandoffSocketEnd` | `wb-hostd` | close the VM's sockets up to a number, and whether the VM stops | I728 |
| `HandoffSocketEnded` | `wb-proxyd` | it closed them, and at a stop its last event record | I728 |

S07-21-vm-end has the last two.

### States

`wb-hostd` keeps one state per socket:

| State | Entered when | `wb-hostd` holds |
|---|---|---|
| offered | it sent `HandoffSocket` to `wb-proxyd` | both ends |
| acknowledged | `HandoffSocketAck` came, and it sent `wb-netd` its end | `wb-netd`'s end |
| serving | `HandoffSocketHeld` came | nothing |
| failed | a deadline passed, or a `HandoffSocketClosed` came before serving | nothing |
| closed | `HandoffSocketClosed` came while serving | nothing |
| replaced | it offered a newer socket for the VM | nothing |
| ended | its `wb-netd` exited or was ended, or `wb-hostd` sent a `HandoffSocketEnd` that covers it | nothing |

The last four are final. A VM has at most one socket that isn't final.
A VM starts or resumes only once its socket is serving. The build VM
has no hand-off socket (S07-18-build-network).

Each wait from offered and from acknowledged has a deadline in code
(`egress.AckTimeout`), and `wb-hostd` holds its copies until then
(S04-12-descriptor-passing). When a socket fails, `wb-hostd` closes
its copies and sends a socket end for it (S07-21-vm-end), so
`wb-proxyd` doesn't keep it open. At a start or restore it refuses the start with
`proxyd-setup-failed` and the check: `ack-timeout`, `held-timeout`, or
`handoff-closed`. For a running VM, "Who a failure is charged to"
says what it does.

### Notices for another socket

`wb-hostd` sorts each `HandoffSocketAck`, `HandoffSocketHeld` and
`HandoffSocketClosed` by the socket it names:

- **Stale:** the socket is in a final state. `wb-hostd` logs it with
  `handoff-notice-stale`, rate-limited per VM, and ignores it.
- **Refused:** `wb-hostd` never offered the socket, or the state
  doesn't wait for the acknowledgment, such as a second one. From
  `wb-proxyd` that is a crash of `wb-proxyd`, and from `wb-netd` it
  ends that `wb-netd` (S04-architecture, "Control channels").

A notice never fails or ends a socket other than the one it names. So
an old socket's notice can't end its replacement's `wb-netd`. A socket
that failed by its deadline holds nothing back, because the next one
has a new number. `HandoffSocketEnded` is never stale
(S07-21-vm-end).

### `wb-proxyd`'s end

`wb-proxyd` checks a `HandoffSocket` as S07-05-stream-hand-off ("From
`wb-hostd`") says. With the socket it accepts, in this order, it:

1. closes the VM's old socket and every stream that came on it;
2. clears the VM's state when the message is fresh (S07-06-name-mapping,
   "Lifetime");
3. sends its version record on the new socket;
4. sends `HandoffSocketAck`;
5. starts the socket's reader.

So no `HandoffSocketClosed` for the socket comes before its
acknowledgment. If step 3 fails, it closes the socket and sends
`HandoffSocketClosed` instead of the acknowledgment.

A close never waits on a write to a peer. A write in progress on a
stream or a socket that closes ends with an error at once. So the
acknowledgment deadline covers these steps alone, and a `wb-netd` that
stops reading can't make another VM's socket miss it.

A `HandoffSocketClosed` or `HandoffSocketEnded` that `wb-proxyd` can't
send ends its control channel, as a write that fails does
(S04-architecture, "Control channels"). So no socket it closed goes
unreported.

### `wb-netd`'s end

`wb-netd` checks its `HandoffSocket` as S04-07-supervision ("New
hand-off sockets") says. Once it holds the end, it sends
`HandoffSocketHeld` with the generation and number. It sends nothing
for an end it refuses. A newer end replaces the old one, whose streams
it resets (`handoff-socket-ended`).

### Isolation between VMs

No fault on a socket ends `wb-proxyd`'s control channel. It ends for a
fault in a message from `wb-hostd`: its form, its socket type, its VM
slot, its generation or its number, as S07-05-stream-hand-off ("From
`wb-hostd`") lists them (I576). `wb-hostd` is the trusted core, so such
a fault is a bug in it. It also ends for `wb-proxyd`'s own faults on
the channel: a notice `wb-hostd` refuses ("Notices for another
socket"), or one it can't send ("`wb-proxyd`'s end").

Anything that goes wrong on a socket after `wb-proxyd` accepted it
closes that socket only, with its streams. `wb-proxyd` reports it in
one `HandoffSocketClosed`, and other VMs' sockets go on.

### Closing a socket

`wb-proxyd` closes a VM's socket, with every stream that came on it,
for one of these rules:

| Rule | Check | Cause | Charged to |
|---|---|---|---|
| `name-report-malformed` | `message-form`, `name-form`, `synthetic-range` | S07-06-name-mapping, "Malformed reports" | `wb-netd` |
| `stream-reset-unsent` | `buffer-full`, `deadline` | a `StreamReset` it can't send (S07-05-stream-hand-off, "Resets") | `wb-netd` |
| `handoff-socket-ended` | `peer-closed`, `control-cut` | `wb-netd` closed its end, or sent control data that was cut | `wb-netd` |
| `handoff-socket-ended` | `receive-failed`, `version-record` | a receive failed with an error, or step 3 above failed | `wb-proxyd` |

For each, it sends one `HandoffSocketClosed` with the rule and the
check, for the VM, generation and number. `wb-netd` resets every
stream of a socket that closed (`handoff-socket-ended`). A socket that
is replaced or ended sends no `HandoffSocketClosed`.

### Who a failure is charged to

A guest can cause each failure charged to `wb-netd`, through a
`wb-netd` it compromised or one it slows down. For one on a running VM,
`wb-hostd` ends the `wb-netd` the socket was given to, with
`handoff-netd-ended` and the
rule or check as the cause. The exit counts toward `wb-netd`'s restart
limit (S04-07-supervision, "Restarting a daemon"). A `held-timeout` is
charged the same way, as `wb-netd` withheld its acknowledgment.

A failure charged to `wb-proxyd` is a fault of `wb-proxyd` or the host,
which no `wb-netd` can cause. For one on a running VM, `wb-hostd` ends
`wb-proxyd` with `handoff-proxyd-ended` and the check. An `ack-timeout`
is charged the same way. The exit counts toward `wb-proxyd`'s restart
limit, and never toward a `wb-netd`'s.

`wb-proxyd` raises its descriptor limit at its start to cover every
descriptor the `wb-netd`s of all VM slots can make it hold at once. Per
slot that is its hand-off socket, two for each stream and one for each
message read but not yet decided under S07-05-stream-hand-off ("Caps"),
and the most one message can carry (S07-05-stream-hand-off, "Message
form"). A margin in code covers its own. If it can't, it logs `proxyd-descriptor-limit` and
exits, as `wb-netd` does for a failed start check (S04-09-netd-start). So
one VM at its caps can't make the other's records arrive at a full
descriptor table.

### Generations and restarts

| Event | Generation | New socket, with a new number | Fresh |
|---|---|---|---|
| a start that isn't a restore | new | for the VM | yes |
| a restore | new | for the VM, with a new `wb-netd` (S04-02-vm-provider, "Restore") | no |
| a `wb-netd` restart | kept | for the VM | no |
| a `wb-proxyd` restart | kept | for every running VM | no |
| a new active period | kept | none: `ActivePeriodStart` only | no |

A socket for a kept generation replaces the old one. After a
`wb-proxyd` restart it holds no socket, and each VM's first sets the
generation and number it holds. S07-06-name-mapping ("Lifetime") says
which `HandoffSocket` is fresh.

### Stream bounds

Each stream's socket is bounded in time where it can stall:

- `wb-netd` keeps its copy of the end it passed to the hold bound
  (S07-05-stream-hand-off, "Transport", `stream-hold-expired`).
- `wb-netd` resets a stream whose guest takes no bytes within a bound
  in code (`guest-write-timeout`). That covers a guest that advertises a
  zero window.
- `wb-proxyd` bounds each write to a stream's socket with a deadline in
  code, longer than `wb-netd`'s guest bound. When it passes, it resets
  the stream and closes its upstream connection
  (`stream-write-timeout`). That covers a `wb-netd` that stops reading.
- When a stream's socket ends, `wb-proxyd` closes the upstream
  connection.

A stream on which neither side sends isn't bounded in time. It holds
its place under the caps of S07-05-stream-hand-off ("Caps") and
S07-02-packet-path ("Per-VM caps").

### Audit

A decision about a stream, a name report or a socket is an egress
decision (SEC10-audit, every egress decision is recorded on the host).
The process that makes it sends it on the VM's event channel with its
rule, rate-limited per rule (S07-02-packet-path, "Events and rate
limits"). That includes `wb-netd`'s hand-off sender. A decision's record
is queued before the bytes it allows are forwarded.

| Process | Rules it records |
|---|---|
| `wb-netd` | `handoff-send-failed`, `handoff-unavailable`, `handoff-socket-refused`, `stream-refused`, `stream-reset`, `guest-write-timeout`, `guest-write-failed`, `proxyd-stream-read-failed`, `handoff-socket-ended`, `proxyd-message-refused` |
| `wb-proxyd` | `handoff-socket-refused`, `name-report-refused`, `stream-handoff-refused`, `fdchan-fd-table-full`, `address-conflict`, `stream-write-timeout`, `stream-end-unrecorded`, and each rule of "Closing a socket" |
| `wb-hostd` | `proxyd-setup-failed`, `handoff-netd-ended`, `handoff-proxyd-ended`, `handoff-end-timeout`, `handoff-end-read-timeout`, `proxyd-records-lost` |

`wb-hostd` writes its own decisions to the audit log itself. These go
only to the daemon log, because they decide nothing about a guest's
traffic: `handoff-notice-stale`, `stream-hold-expired`,
`stream-reset-unknown`, `vm-fresh-start` and `active-period-start`.
`proxyd-descriptor-limit` is a start check of `wb-proxyd`. Its log
line reaches `wb-hostd` on the log pipe, and the exit counts toward the
restart limit (S04-07-supervision, "Restarting a daemon").

Two processes record `handoff-socket-ended` and
`handoff-socket-refused`, each with its own meaning. The record's
`sender`, which `wb-hostd` stamps from the channel, tells them apart,
so a reader keys on the sender and the rule together. A `wb-netd` can
only send records about its own VM. So a compromised one can suppress
its own refusals, but not a decision of `wb-proxyd` or `wb-hostd`.

## Decisions

### A number for each socket

`wb-hostd` numbers each hand-off socket, and every message about one
names it.

- **Why:** a replacement has the same VM and generation as the socket
  it replaces, so a late notice of the old one could end the new one.
- **Rejected:** a new generation for each socket. The generation then
  stops meaning one run of the VM.
- **Decided:** I728 (2026-11-10).

### `wb-netd` acknowledges its end

- **Why:** `wb-hostd` must keep its copy until `wb-netd` holds it
  (S04-12-descriptor-passing), and start the VM only once both ends
  are held.
- **Rejected:** start the VM once `wb-netd`'s end is sent. The end
  could arrive dead, and the VM would run without egress.
- **Decided:** I729 (2026-11-10), built with I728.

### A socket's failure stays with its VM

After `wb-proxyd` accepts a socket, a failure on it closes that socket
only and is reported. `wb-hostd` ends the daemon the failure is charged
to. No fault on a socket ends the control channel.

- **Why:** one `wb-proxyd` serves every VM (T14-shared-proxyd). A
  socket fault a guest can cause is the doing of one VM's `wb-netd`.
  Reporting it lets `wb-hostd` end that `wb-netd` instead of leaving a
  VM without egress and without a record.
- **Rejected:**
  - End the control channel on any socket failure, as for a refused
    `HandoffSocket` (option B). Every VM then loses its network for one
    VM's fault.
  - Close the socket and tell no one (option C). `wb-hostd` then
    doesn't know the VM has no egress, and no audit record says why.
- **Decided:** I754 (2026-11-11), option A of B754-handoff-contract.
  I576 decided the refused `HandoffSocket` (2026-11-07), and I571 an
  unsent reset (2026-11-10).

**Status:** Draft
