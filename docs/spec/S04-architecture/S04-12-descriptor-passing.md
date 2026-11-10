# S04-12 - Passing Descriptors

**Purpose:** How one host process passes an open socket to another:
who holds it when, how the receiver shows it holds it, when the sender
closes its copy, and what a lost or bad acknowledgment does.

**Requirements:** SEC08-proj-isolation, SEC13-bounded-resources

**Tests:** C11-host-daemons

Brief: B754-handoff-contract

## Design

### Why a sender keeps its copy

On macOS 27 the kernel can collect a Unix domain socket that is in
flight in an `SCM_RIGHTS` message while no process holds another
descriptor of it. The receiver then gets a dead socket: a read returns
a record of 0 bytes, and a send fails with `EINVAL` (I637).

So a process that passes a socket keeps its own copy until the
receiver shows it holds it (I647, I648). The proof is one of:

- an acknowledgment from the receiver that names the descriptor;
- the receiver's first record or byte on the passed socket.

The rule applies on every platform, so the code has one path. These
descriptors are exempt, because they are never in flight without a
holder:

- a pipe, such as the payload pipes of the control channels
  (S04-architecture, "Control channels");
- a descriptor a process inherits at spawn, such as a daemon's control
  and event channels and the data disk shim's descriptors.

### The legs

| Leg | Passed | Proof the receiver holds it | Bound on the kept copy |
|---|---|---|---|
| `wb-vmd` to `wb-hostd` | a guest connection or the packet transport (`HandoffMessage`) | `HandoffAck` | `wb-vmd`'s connect timeout |
| `wb-hostd` to `wb-proxyd` | its end of a VM's hand-off socket (`HandoffSocket`) | `HandoffSocketAck` | the setup deadline of S07-20-handoff-sockets |
| `wb-hostd` to `wb-netd` | the other end (`HandoffSocket`) | `HandoffSocketHeld` | the same deadline |
| `wb-netd` to `wb-proxyd` | one end of a stream's socketpair (`Stream`) | `wb-proxyd`'s first byte on it, or a `StreamReset` for it | `StreamHoldTimeout` |

S04-02-vm-provider ("Hand-off on macOS") has the first leg's records
and rules. S07-20-handoff-sockets has the middle two, and
S07-05-stream-hand-off the last. Each bound is a value in code, named
here so a reader can find it.

### States of a passed descriptor

A sender's copy is in one of these states:

| State | Entered when | Left when |
|---|---|---|
| kept | the send returned | the proof arrives, or the bound passes |
| released | the proof arrived | never: the copy is closed |
| expired | the bound passed | never: the copy is closed |

A send that fails leaves nothing kept. The sender closes its copy at
once and handles the failure as its leg says.

When the bound passes, the sender closes its copy and logs it with the
leg's rule. An expired copy isn't a refusal on the last leg, where a
stream whose client speaks first can be idle for longer than the bound
(`stream-hold-expired`). On the others it fails what waited on it.

A sender keeps each copy under the key the proof names: the request of
the first leg, the hand-off socket's number on the middle two, the
stream on the last. One proof releases one copy.

### Acknowledgments are input

The receiver of an acknowledgment checks it as input from another
process. It doesn't trust that the sender of the acknowledgment is
honest. For each case:

| The acknowledgment is | The receiver |
|---|---|
| lost | closes the copy when the bound passes |
| of the wrong form | refuses it, logs it, and reads on |
| for no kept copy: a duplicate, or one that comes after the bound | refuses it, logs it, and reads on |

So one bad acknowledgment never stops the reader, and the next hand-off
is acknowledged as usual. On a control channel, a record of the wrong
form follows the channel's own rule instead, which treats it as a crash
of the daemon that sent it (S04-architecture, "Control channels").

A reader that only reads acknowledgments, such as `wb-vmd`'s, ends in
two cases:

- its peer closed its end;
- a run of failed receives in a row reaches a bound in code. A receive
  fails on an error from `recvmsg`, or on control data that was cut.

It logs the end with its rule and the number of copies it keeps. Each
kept copy then closes when its bound passes. A receive that succeeds
resets the count.

### Records and the channel

Each leg sends one record per message, with at most one descriptor, on
a channel of `internal/platform/fdchan` (S12-01-platform-interfaces,
"One package for the descriptor channels"). The interface doesn't
change for this page: the caller keeps the copy, not `fdchan`.

## Decisions

### Keep the copy until the receiver holds it

A sender keeps its copy of a passed socket until an acknowledgment or
the receiver's first record or byte, bounded by a deadline.

- **Why:** on macOS 27 a socket in flight with no holder can arrive
  dead (I637). Keeping the copy costs a descriptor for a short time.
- **Rejected:**
  - Close at once and retry a dead socket. The receiver can't tell a
    dead socket from a peer that closed, so it would need a second
    proof anyway.
  - Pass every socket at spawn, by inheritance. Hand-off sockets and
    streams are made while the daemons run.
- **Decided:** I637 (2026-11-10), built in I647 and I648.

### One bad acknowledgment never stops the reader

A wrong, duplicate or late acknowledgment is refused and skipped. A
dedicated reader ends only when its peer closes, or after a run of
failed receives.

- **Why:** an ended reader leaves every later copy kept to its bound.
  On the first leg each holds a connect place in `wb-vmd` for the full
  bound (I751). Skipping a bad record fails closed as well, because an
  unacknowledged copy still closes at its bound.
- **Rejected:** end the reader at the first bad record. That turns one
  bad record into a slower VM until `wb-vmd` restarts.
- **Decided:** I754, in this spec (B754-handoff-contract).

**Status:** Draft
