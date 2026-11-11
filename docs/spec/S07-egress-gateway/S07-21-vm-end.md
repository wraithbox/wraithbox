# S07-21 - A VM's End in wb-proxyd

**Purpose:** How `wb-hostd` ends a VM's hand-off sockets in the one
`wb-proxyd`, and how the VM's last event records reach its stop record
without one VM's stop or backlog taking the other VM's network down.

**Requirements:** SEC08-proj-isolation, SEC10-audit,
SEC13-bounded-resources

**Tests:** C02-streams-and-tls, C11-host-daemons

Brief: B754-handoff-contract

## Design

### The end exchange

`HandoffSocketEnd` names the VM, the generation and a socket number. It
also says whether the VM stops. `wb-proxyd` closes every socket of the
VM and generation whose number is at or below the one named, with every
stream that came on them. It answers `HandoffSocketEnded` with the same
VM, generation and number, also when it holds none of them.

`wb-hostd` sends it in two cases:

- **A socket leaves service** while the VM runs: it failed by a
  deadline, or its `wb-netd` exited or was ended. The message names
  that socket.
- **The VM stops**, is saved, or its start is refused. The message names
  the highest number `wb-hostd` offered for the generation, whatever
  each socket's state, and says the VM stops.

The exchange has its own wait in `wb-hostd`, keyed by VM, generation
and number, apart from the socket states of S07-20-handoff-sockets.
Every socket it covers becomes ended when it is sent. So
`HandoffSocketEnded` is never stale. One that answers no wait is
refused, as a crash of `wb-proxyd` (S04-architecture, "Control
channels").

### Order at a VM stop

`wb-hostd` sends `HandoffSocketEnd` before it stops the VM's `wb-netd`
(S04-09-netd-start, "Stop"). A `HandoffSocketClosed` for `peer-closed`
that the stop then causes names an ended socket, so it is stale and
charges no one.

`wb-proxyd` then, in this order:

1. closes the VM's sockets and streams up to the number;
2. waits for each of those streams' handlers to queue its last event
   record, at most a bound in code shorter than the answer's deadline;
3. counts each handler that didn't in a run with the rule
   `stream-end-unrecorded`, and queues the counts of the VM's open runs;
4. answers `HandoffSocketEnded` with the sequence number of its last
   queued event record for the VM, or 0 when it sent none.

Step 2 can't wait on a peer, because a close ends every write in
progress (S07-20-handoff-sockets, "`wb-proxyd`'s end"). No step waits
on the event channel's delivery. So an honest `wb-proxyd` answers
within the deadline whether or not `wb-hostd` reads its channel.

### Events in band

Each event record from `wb-proxyd` has a sequence number, one more than
the last (S04-08-records-and-bounds, "Event records"). `wb-hostd`
reads `wb-proxyd`'s channel up to the sequence the answer names, then
ends the VM's events (`Hub.End`). So the VM's stop record has every
count `wb-proxyd` sent, and no record of the VM comes after its end.

That read runs under the stop allowance of S09-11-audit ("Telling the
senders"). It reads past the holds, for at most as many records as the
allowance gives a `wb-netd`. Records of another VM read under it count
toward that VM's budget as usual. An honest `wb-proxyd`'s queue and
socket buffer fit in the allowance. If the sequence isn't reached by
then, `wb-hostd` ends the VM's events anyway and logs
`event-stop-allowance`.

### When `wb-proxyd` may name a generation

`wb-proxyd` may name a VM and generation on its event channel from
before the first `HandoffSocket` of that generation up to the sequence
its stopping `HandoffSocketEnded` names. `wb-hostd` opens the window
before it sends that `HandoffSocket`.

A record that names the VM and generation outside the window ends
`wb-proxyd` with `event-record-invalid`. The one exception is a record
at or below that sequence that comes after the allowance ran out.
`wb-hostd` drops it and logs it with `event-stop-allowance`, because
`wb-proxyd` sent it in the window.

### Deadline

The answer has the same deadline as an acknowledgment (`egress.AckTimeout`).
It covers the steps above, which close sockets and streams and queue
records. If it passes, `wb-hostd` ends `wb-proxyd` with
`handoff-end-timeout`, and the exit counts toward its restart limit.
`wb-hostd` then reads what is left on `wb-proxyd`'s channel under the
allowance, and ends the VM's events.

### Records lost with `wb-proxyd`

When `wb-proxyd` exits or is ended with records unsent, those records
are lost. `wb-hostd` writes one audit record for each VM it served,
with the rule `proxyd-records-lost`, the exit's rule, and the last
sequence it read. A VM's stop record names the same. So a reader knows
the counts after that sequence may be missing.

### The hold of I741

S09-11-audit ("Telling the senders") may hold `wb-proxyd`'s channel
for one VM's budget. The end doesn't depend on that hold. `wb-proxyd`
answers without waiting on delivery, and `wb-hostd` reads past a hold
under the allowance. Without the hold, it reads as usual. Whether one
VM's budget may hold the other VM's network while both run is I741's.

## Decisions

### A VM's end is acknowledged

- **Why:** a `wb-proxyd` record for a VM whose events have ended can't
  be attributed. Ending them after the answer keeps every count in the
  stop record.
- **Rejected:** end the VM's events at once. A record `wb-proxyd` still
  queued for the VM would then name a VM that isn't running.
- **Decided:** I512, decision 4 (2026-10-31).

### The end is its own exchange, before `wb-netd` stops

- **Why:** an end that waits on a socket's state is lost when that
  socket was closed or failed first. `wb-hostd` then ends the shared
  `wb-proxyd` for a VM that stopped normally (I754, code and security
  reviews).
- **Rejected:** answer the end only for a socket that is serving. A
  malformed report or a refused start then ends `wb-proxyd` at the
  stop.
- **Decided:** I754 (2026-11-11), with option A of
  B754-handoff-contract.

### The answer names the last event record

- **Why:** the control channel and the event channel have no order
  between them. Naming the sequence puts the end in the event stream,
  so `wb-hostd` ends the VM's events after its last record and not
  before.
- **Rejected:**
  - Answer once the counts are queued, and end the VM's events at the
    answer. A queued record then names a stopped VM, and
    `event-record-invalid` ends `wb-proxyd`.
  - Answer once the counts are delivered. A held channel then delays
    the answer, and `handoff-end-timeout` ends `wb-proxyd`.
- **Decided:** I754 (2026-11-11), with option A of
  B754-handoff-contract.

**Status:** Draft
