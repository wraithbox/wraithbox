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

`HandoffSocketEnd` names the VM, the generation, a socket number, and
whether the VM stops. `wb-proxyd` closes every socket of the VM and
generation whose number is at or below the one named, with every
stream that came on them. It answers `HandoffSocketEnded` with the same
four values, also when it holds none of those sockets.

There are two kinds, and `wb-hostd` sends each at most once:

- **A socket end** doesn't stop the VM. `wb-hostd` sends it for a
  socket that wasn't final before one of these: it fails by a deadline,
  or its `wb-netd` exits or is ended. It names that socket. `wb-proxyd`
  runs step 1 of "Order at a VM stop" only, and its answer has the
  sequence 0. `wb-hostd` refuses any other value.
- **A stop end** says the VM stops. `wb-hostd` sends it once for the
  generation when the VM stops, is saved, or its start is refused. It
  names the highest number `wb-hostd` offered for the generation,
  whatever each socket's state. `wb-proxyd` runs every step.

`wb-hostd` keeps one wait for each end it sent, keyed by VM,
generation, number and the stop flag, apart from the socket states of
S07-20-handoff-sockets. So a socket end and a stop end for the same
number have their own waits. That covers these cases:

- a socket fails, and then the VM stops;
- a start's socket fails, and the start is refused;
- a `wb-netd` exits just before its VM's stop end is sent.

When an end is sent, each socket it covers that isn't final becomes
ended. A socket already final keeps its state. A socket end is sent
only for a socket that wasn't final before, so a `wb-netd` that exits
after the stop end causes none. `HandoffSocketEnded` is never stale. One that matches no
wait is refused, as a crash of `wb-proxyd` (S04-architecture, "Control
channels").

### Order at a VM stop

`wb-hostd` sends the stop end before it stops the VM's `wb-netd`
(S04-09-netd-start, "Stop"). A `HandoffSocketClosed` for `peer-closed`
that the stop then causes names an ended socket, so it is stale and
charges no one. `wb-netd` sees its socket close first, so an honest
`wb-netd` records `handoff-socket-ended` at each stop. The stop record
after it tells that apart from a failure.

`wb-proxyd` then, in this order:

1. closes the VM's sockets and streams up to the number, and cancels
   each of their handlers' upstream dial, name lookup and secret fetch
   in progress;
2. waits for every running handler of the VM and generation to queue
   its last event record, those of streams an earlier socket end closed
   included, at most a bound in code shorter than the answer's deadline;
3. counts each handler that didn't in a run with the rule
   `stream-end-unrecorded`, and queues the counts of the VM's open runs;
4. answers `HandoffSocketEnded` with the sequence number of its last
   queued event record that names the VM and generation, or 0 when it
   sent none.

Step 2 can't wait on a peer, because a close ends every write in
progress (S07-20-handoff-sockets, "`wb-proxyd`'s end"). No step waits
on the event channel's delivery or on room in its queue. The records
of a close go into the queue even at its cap, as S09-11-audit
("Telling the senders") bounds them. So an honest `wb-proxyd` answers within the
deadline whether or not `wb-hostd` reads its channel.

After its stop answer, `wb-proxyd` queues no record that names the VM
and generation, and it records nothing for an ended generation. A
handler that finishes late drops its record, and the
`stream-end-unrecorded` count stands for it. After `wb-hostd` sends the
stop end, it sends `wb-proxyd` no `PolicyChange` or `ActivePeriodStart`
for the VM, and no message that names the ended generation. The
placeholder set and the CA messages, `CaRemove` included, don't name a
generation and stay allowed.

### Events in band

Each event record has a sequence number (S04-08-records-and-bounds,
"Event records"). `wb-hostd` reads `wb-proxyd`'s channel up to the
sequence the stop answer names, then ends the VM's events (`Hub.End`).
So the VM's stop record has every count `wb-proxyd` sent, and no record
of the VM comes after its end.

`wb-hostd` refuses a stop answer whose sequence is below the one of
the last record that names the VM and generation it read from that
`wb-proxyd`. A refused answer is a crash of `wb-proxyd`. A high
sequence isn't refused, because the read below ends at its own bound.

That read runs under the stop allowance of S09-11-audit ("Telling the
senders"). It reads past the holds. Its bound is as many records as the
allowance gives a `wb-netd`, plus the records the VM's closes may queue
past the cap. So the read's last sequence is the last one read when the
answer came plus that bound. Records of another VM read under it count
toward that VM's budget as usual. The read ends at the first of:

- the named sequence;
- the read's last sequence, logged with `event-stop-allowance`;
- the end of `wb-proxyd`'s channel;
- a deadline in code, which ends `wb-proxyd` with
  `handoff-end-read-timeout`. The exit counts toward its restart limit.

`wb-hostd` then ends the VM's events. When the read stopped short, the
stop record names the rule, the named sequence and the last one read,
so a reader knows the records between them are missing.

### When `wb-proxyd` may name a generation

`wb-proxyd` may name a VM and generation on its event channel from
before the first `HandoffSocket` of that generation up to the sequence
its stop answer names. `wb-hostd` opens the window before it sends that
`HandoffSocket`.

A record that names the VM and generation outside the window ends
`wb-proxyd` with `event-record-invalid`. The one exception is a record
that comes after the read stopped short, at or below the lower of the
named sequence and the read's last sequence.
`wb-hostd` drops it and logs it with `event-stop-allowance`, because
`wb-proxyd` sent it in the window.

### Deadline

The answer has the same deadline as an acknowledgment (`egress.AckTimeout`).
It covers steps 1 to 3, which close sockets and streams and queue
records. If it passes, `wb-hostd` ends `wb-proxyd` with
`handoff-end-timeout`, and the exit counts toward its restart limit.

### When `wb-proxyd` exits

A wait ends with the `wb-proxyd` it was sent to. When `wb-proxyd` exits
or is ended, every wait for an end it didn't answer ends too. For a
stop end, `wb-hostd` reads what is left on the channel under the
allowance and ends the VM's events. A new `wb-proxyd` never gets an end
for a socket it wasn't given. While no `wb-proxyd` runs, a VM's stop
sends no end and ends the VM's events at once.

When `wb-proxyd` exits or is ended with records unsent, those records
are lost. `wb-hostd` writes one audit record for each VM it served,
with the rule `proxyd-records-lost`, the exit's rule, and the last
sequence it read. A VM's stop record names the same.

### One VM doesn't wait on another

`wb-proxyd` decides the messages about one VM in order. It runs each
VM's end and each socket's steps apart from its control loop. So a wait
for one VM never delays a message about another VM.

`wb-hostd` keeps each wait on its own as well. After a `wb-proxyd`
restart, each running VM's new socket goes through its states alone.
A `wb-netd` that withholds `HandoffSocketHeld` fails only its own VM's
socket.

### The hold of I741

S09-11-audit ("Telling the senders") may hold `wb-proxyd`'s channel
for one VM's budget. The end doesn't depend on that hold. `wb-proxyd`
queues the end's records past its cap and answers without waiting on
delivery, and `wb-hostd` reads past a hold under the allowance. Without
the hold, it reads as usual. Whether one VM's budget may hold the other
VM's network while both run is I741's.

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

### The stop flag keys the wait

- **Why:** a socket end and the stop end can name the same number at
  once. With the flag in the key and the answer, each answer finds its
  own wait.
- **Rejected:** let the stop end take over a pending socket end. Then
  `wb-hostd` must tell which answer came for which, and the late one is
  a refused answer that ends `wb-proxyd`.
- **Decided:** I754 (2026-11-11), round 3 of the reviews.

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

### The end's records may pass the queue's cap

- **Why:** at its cap, a queue waits for `wb-hostd` to read. A hold for
  the other VM's budget would then make an honest `wb-proxyd` miss the
  answer's deadline. The per-VM caps bound what the end adds.
- **Rejected:** read past the holds from when the end is sent. Records
  of the other VM could still fill the queue first.
- **Decided:** I754 (2026-11-11), round 3 of the reviews.

**Status:** Draft
