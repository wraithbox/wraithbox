# S07-20 - Hand-off Sockets

**Purpose:** The life of one VM's hand-off socket: its identity, the
acknowledgments of both ends, how it closes and ends, what each restart
does to it, and which of its decisions reach the audit log.

**Requirements:** SEC08-proj-isolation, SEC10-audit,
SEC13-bounded-resources

**Tests:** C02-streams-and-tls, C11-host-daemons

Brief: B754-handoff-contract

![wb-hostd makes each hand-off socket, passes one end to wb-proxyd and, after its acknowledgment, the other to the VM's wb-netd. Each end acknowledges with the socket's number. wb-proxyd reports a socket it closes, and acknowledges the end of one at a VM stop.](S07-20-handoff-sockets.svg)

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

These are in `proto/wraithbox/egress/v1/control.proto`:

| Message | From | Means |
|---|---|---|
| `HandoffSocket` | `wb-hostd` | here is an end, with its VM, generation and number |
| `HandoffSocketAck` | `wb-proxyd` | it holds the end it names |
| `HandoffSocketHeld` | `wb-netd` | it holds the end it names |
| `HandoffSocketClosed` | `wb-proxyd` | it closed the socket it names, with a rule and a check |
| `HandoffSocketEnd` | `wb-hostd` | the VM stops: close its socket |
| `HandoffSocketEnded` | `wb-proxyd` | it closed the socket and sent its counts |

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
| ended | `HandoffSocketEnded` came at a VM stop | nothing |

The last four are final. A VM has at most one socket that isn't final.

Each wait from offered and from acknowledged has a deadline in code
(`egress.AckTimeout`), and `wb-hostd` holds its copies until then
(S04-12-descriptor-passing). When a socket fails, `wb-hostd` closes
its copies. At a start or restore it refuses the start with
`proxyd-setup-failed` and the check: `ack-timeout`, `held-timeout`, or
`handoff-closed`. For a running VM, it ends the VM's `wb-netd`, which
counts toward the restart limit (S04-07-supervision, "Restarting a
daemon").

A VM starts or resumes only once its socket is serving. The build VM
has no hand-off socket (S07-18-build-network).

### Notices for another socket

A `HandoffSocketAck`, `HandoffSocketHeld`, `HandoffSocketClosed` or
`HandoffSocketEnded` that names a socket in a final state, or one
`wb-hostd` never offered, is stale. `wb-hostd` logs it with
`handoff-notice-stale` and ignores it. It never fails or ends the
socket that is current. So is a `HandoffSocketAck` or
`HandoffSocketHeld` that the socket's state doesn't wait for.

So an old socket's notice can't end its replacement's `wb-netd`. A
socket that failed by its deadline holds nothing back: the next one has
a new number.

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

### `wb-netd`'s end

`wb-netd` checks its `HandoffSocket` as S04-07-supervision ("New
hand-off sockets") says. Once it holds the end, it sends
`HandoffSocketHeld` with the generation and number. It sends nothing
for an end it refuses. A newer end replaces the old one, whose streams
it resets (`handoff-socket-ended`).

### Isolation between VMs

Only a fault in a message from `wb-hostd` ends `wb-proxyd`'s control
channel, as `control.proto` says: its form, its socket type, its VM
slot, or a generation or number older than held (I576). `wb-hostd` is
the trusted core, so such a fault is a bug in it.

Anything that goes wrong on a socket after `wb-proxyd` accepted it
closes that socket only, with its streams. `wb-proxyd` reports it in
one `HandoffSocketClosed`, and other VMs' sockets go on.

### Closing a socket

`wb-proxyd` closes a VM's socket, with every stream that came on it,
for one of these rules:

| Rule | Checks | Cause |
|---|---|---|
| `name-report-malformed` | `message-form`, `name-form`, `synthetic-range` | S07-06-name-mapping, "Malformed reports" |
| `stream-reset-unsent` | `buffer-full`, `deadline` | a `StreamReset` it can't send (S07-05-stream-hand-off, "Resets") |
| `handoff-socket-ended` | `peer-closed`, `receive-failed`, `version-record` | `wb-netd` closed its end, a receive failed or cut control data, or step 3 above failed |

For each, it sends one `HandoffSocketClosed` with the rule and the
check, for the VM, generation and number. A guest can cause each of
them, through a `wb-netd` it compromised or one it slows down. So
`wb-hostd` treats each the same. It ends the VM's `wb-netd` and counts
the exit toward the restart limit. `wb-netd` resets every stream of a
socket that closed (`handoff-socket-ended`).

A socket that is replaced or ended sends no `HandoffSocketClosed`.

### A VM stops

When a VM stops or is saved, `wb-hostd` sends `HandoffSocketEnd` for
its socket. `wb-proxyd`:

1. closes the socket and every stream that came on it;
2. sends the counts of the VM's open runs on the event channel
   (S04-08-records-and-bounds, "Event records");
3. answers `HandoffSocketEnded` for the same socket, also for one it no
   longer holds.

`wb-hostd` ends the VM's events (`Hub.End`) only after that answer, or
after `wb-proxyd` exits. So the VM's stop record has every count.
`HandoffSocketEnd` keeps the VM's copy of the name mapping
(S07-06-name-mapping, "Lifetime").

The answer has the same deadline as an acknowledgment. If it passes,
`wb-hostd` ends `wb-proxyd` with `handoff-end-timeout`, and the exit
counts toward its restart limit. An honest `wb-proxyd` answers within
it, as its event writes are bounded (S04-08-records-and-bounds).

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
generation and number it holds. `fresh` stays set on each
`HandoffSocket` after a fresh start until `wb-proxyd` acknowledges
one, so a socket that fails is followed by a fresh one again.

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
limits"). That includes `wb-netd`'s hand-off sender.

| Process | Rules sent as events |
|---|---|
| `wb-netd` | `handoff-send-failed`, `handoff-unavailable`, `stream-refused`, `stream-reset`, `guest-write-timeout`, `guest-write-failed`, `proxyd-stream-read-failed`, `handoff-socket-ended`, `proxyd-message-refused` |
| `wb-proxyd` | `handoff-socket-refused`, `name-report-refused`, `stream-handoff-refused`, `address-conflict`, `stream-write-timeout`, and each rule of "Closing a socket" |

`wb-hostd` writes its own decisions to the audit log itself:
`proxyd-setup-failed`, `handoff-end-timeout`, and the end of a
`wb-netd` for a `HandoffSocketClosed`. These go only to the daemon log,
because they decide nothing about a guest's traffic:
`handoff-notice-stale`, `stream-hold-expired`, `stream-reset-unknown`,
`vm-fresh-start` and `active-period-start`.

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
only and is reported. Only a fault in `wb-hostd`'s message ends the
control channel.

- **Why:** one `wb-proxyd` serves every VM (T14-shared-proxyd). A
  socket fault is the doing of one VM's `wb-netd`. Reporting it lets
  `wb-hostd` end that `wb-netd` instead of leaving a VM without egress
  and without a record.
- **Rejected:** end the control channel on any socket failure, as for a
  refused `HandoffSocket`. Every VM then loses its network for one
  VM's fault.
- **Decided:** I754, in this spec (B754-handoff-contract). I576 decided
  the refused `HandoffSocket` (2026-11-07), and I571 an unsent reset
  (2026-11-10).

### A VM's end is acknowledged

- **Why:** a `wb-proxyd` record for a VM whose events have ended can't
  be attributed. Ending them after the answer keeps every count in the
  stop record.
- **Rejected:** end the VM's events at once. A record `wb-proxyd` still
  queued for the VM would then name a VM that isn't running.
- **Decided:** I512, decision 4 (2026-10-31).

**Status:** Draft
