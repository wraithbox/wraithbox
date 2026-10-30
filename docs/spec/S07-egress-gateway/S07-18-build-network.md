# S07-18 - Build Network

**Purpose:** The build VM's network: what its `wb-netd` serves, the
one connection to the guest's port 22, and its refusal for session
VMs.

## Design

### What the build VM's `wb-netd` serves

The image build's one SSH session needs a network
(S06-02-image-build, "Provisioning stages"). The build VM's `wb-netd`
gives it the least that session needs:

- DHCP and ARP, as for any VM (S07-02-packet-path, "Addressing");
- one TCP connection from its stack to the guest's port 22, on
  `wb-hostd`'s request ("The connection");
- no egress.

For no egress, it resets every TCP connection the guest opens, and
drops all other UDP, DNS included. Each is logged with the rule
`build-egress-refused`, rate-limited as S07-02-packet-path, "Events
and rate limits" says. It doesn't hold a hand-off socket to `wb-proxyd`:
`wb-hostd` gives it none, and `wb-proxyd` refuses one for the build VM
with `handoff-socket-refused` (S07-05-stream-hand-off).

### Build mode

`wb-netd` learns whether its VM is the build VM from its start
message, which also names its VM and generation.

- The mode `build` marks the build VM. A missing or unknown mode makes
  it a session VM's `wb-netd`.
- The mode is fixed for the life of the process. A control message
  that names a mode is refused, and logged with `build-mode-refused`.

### The connection

The request comes on `wb-netd`'s control channel and names a VM and a
generation. `wb-netd` refuses it, and logs `build-connect-refused`
with the check that failed, when:

- its mode isn't `build`;
- the VM or the generation isn't its own;
- a connect is already running;
- it has already handed a socket to `wb-hostd`.

Otherwise it:

1. opens a TCP connection from the gateway address to the guest's
   leased address, port 22;
2. tries again after a reset or no answer, as long as the connect
   deadline allows, because `sshd` answers only some time after the
   guest starts (X17-image-build);
3. once the connection is established, logs `build-connect` with the
   VM, the generation and the leased address;
4. relays it over a Unix domain `SOCK_STREAM` socketpair, as for a
   stream (S07-05-stream-hand-off);
5. sends the other end to `wb-hostd` on its control channel, in a
   message with exactly one descriptor.

Only an established connection counts. Each `wb-netd` process hands
at most one socket to `wb-hostd`. When the deadline passes first, it
logs `build-connect-timeout`, tells `wb-hostd`, and the build fails.

| Limit | Value | Rule |
|---|---|---|
| Wait between tries | 1 s | none |
| Connect deadline, from the request | 3 minutes | `build-connect-timeout` |

`wb-hostd` receives the socket as S07-05-stream-hand-off, "Message
form", says for `wb-proxyd`, and hands it to `wb-build-ssh`
(S04-05-helper-programs). `wb-netd` doesn't open an upstream
connection on this path.

`wb-hostd` never restarts the build VM's `wb-netd`. When it exits, the
image build fails with its exit reason (S04-07-supervision). So one
build gets at most one socket.

### What the refusal guards against

The session VM refusal guards against a confused request. A
compromised `wb-hostd` writes the start message, so it could start a
session VM's `wb-netd` in build mode. It still has no password for
`wbadmin`, which only `wb-guestadmin` holds (S15-04-guest-admin), and
the image build turns Remote Login off before it seals the image
(S06-02-image-build).

## Decisions

### One connection into the build guest

The build VM's `wb-netd` opens one connection to the guest's port 22,
and a session VM's refuses to.

- **Why:** the provisioning boot installs `wb-guestd` over SSH, before
  any host-guest socket answers. The build network gives that session
  a path with no egress, and keeps every session VM without a path
  into its guest.
- **Decided:** I139 and I165.

**Status:** Draft
