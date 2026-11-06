# S04-09 - Starting `wb-netd`

**Purpose:** How `wb-hostd` starts and stops each VM's `wb-netd`, what
the program inherits, its profile, and what it does before its start
message.

## Design

### Start interface

`wb-hostd` starts one `wb-netd` per VM run, once it holds the VM's
packet transport (S07-02-packet-path, "Packet transport"). It starts it
with no arguments and an empty environment. `wb-netd` inherits these
descriptors, and no other:

| Descriptor | What it is |
|---|---|
| 2 | the pipe of its log lines to `wb-hostd` (S04-03-self-sandboxing, "Daemon logs") |
| 3 | its control channel (S04-architecture, "Control channels") |
| 4 | the VM's packet transport |
| 5 | its accounting channel (S04-08-records-and-bounds) |
| 6 | its event channel |

Descriptors 0 and 1 are `/dev/null`. After its confinement it refuses
any open descriptor above 6. Then it checks that each of 2 to 6 is
open, and that 3 to 6 are connected `AF_UNIX` datagram sockets. It exits with
`netd-descriptor-invalid` when one is wrong, and with `netd-arguments`
when it got an argument. It exits 0 with `netd-stopped` only when
`wb-hostd` closes the control channel. Any other end, the packet
transport's included, is logged with `netd-failed`, and it exits 1.

The hand-off socket to `wb-proxyd` isn't among them. It comes on the
control channel, after the start message (S04-07-supervision, "New
hand-off sockets"). `wb-proxyd` doesn't run yet (I512), so for now no
`wb-netd` gets one. Each stream hand-off then fails because there is no
socket, and no stream leaves the VM.

### The held transport

`wb-hostd` holds its copy of the packet transport in a type without a
read or write path (S04-07-supervision, "A new `wb-netd`"). Besides
closing it, the type can only start a child with the descriptor in one
slot of the child's inherited files. After the start, a failed one
included, the slot is empty again. The descriptor never reaches
`wb-hostd`'s own code.

### Start message and first policy

`wb-hostd` and `wb-netd` exchange version records on the control
channel. `wb-hostd` then sends `NetdStart` and the first policy, the
static one of the VM's start (S07-01-per-vm-policy), and waits
for its acknowledgment. If a step before the acknowledgment fails,
`wb-hostd` stops `wb-netd` and refuses the VM start with
`netd-start-failed`.

`NetdStart` holds the link's addressing: the guest's and the gateway's
MAC address, the subnet, the gateway and the leased address. Each VM
has its own link. Every VM gets 192.168.127.0/24, the gateway .1 and
the lease .2. The gateway's MAC is the guest's with its last byte
inverted, another unicast address of the same kind.

`wb-netd` refuses with the check `addressing` a MAC that isn't a
unicast 6-byte address, and the same MAC twice. It also refuses a
prefix length outside 8 to 30, a subnet with host bits set, and a
gateway or leased address that isn't a host address of the subnet or
is the other's. `wb-hostd` checks the guest's MAC and loads the policy
before the VM starts, so the transport isn't held unread while it reads
the policy file.

### Drain before the start

`wb-netd` reads the transport from the moment it has checked it. Until
its start message arrives, it drops every frame with the rule
`link-not-started`. A guest that floods frames before and during the
start then doesn't fill the socket and stall its NIC.

### Profile

The `wb-netd` profile is `(deny default)` alone, without parameters
(S04-03-self-sandboxing, SEC12-least-privilege). The descriptors it
inherited work without a rule. It doesn't open files, make sockets,
reach services or start programs. It loads the local time zone before
it confines itself, so that its log doesn't read a file. In its tests,
a confined `wb-netd` serves a DHCP lease, a DNS answer and a stream
hand-off with this profile. A rule added to it is a change to this
page first.

### No restart yet

Until V1-M6 (I480) `wb-hostd` restarts no daemon, and it starts
`wb-netd` itself, without `wb-launcher`. `wb-hostd` has no profile
before then, so `wb-netd` can still confine itself.

- **Exit.** When a VM's `wb-netd` exits during a run, `wb-hostd` stops
  the VM with the rule `netd-exited`. The stop is recorded as a failure.
- **Ended.** A channel reader that refuses what `wb-netd` sent ends it
  with `netd-ended`, and the VM stops the same way.
- **Stop.** At the VM's stop, `wb-hostd` closes the control channel and
  waits 10 s for the exit. It kills a `wb-netd` that doesn't exit with
  `netd-stop-timeout`. It then reads what is left on the accounting
  and event channels, and only then ends the VM's event stream and
  closes its copy of the transport.
- **Hang.** `wb-hostd` doesn't check that a running `wb-netd` is live.
  One that hangs leaves its VM without a network, which fails closed.
  Liveness checks come with the restarts (I480).

An exit while the VM is already stopping, by request or forced, doesn't
take over that stop's rule. The VM manager has no restore path yet, so
each start is a new run with a new `wb-netd`. `wb-netd` refuses a second
`NetdStart`, so a new transport can't reach a running one.

**Status:** Draft
