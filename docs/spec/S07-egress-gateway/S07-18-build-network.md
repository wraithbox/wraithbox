# S07-18 - Build Network

**Purpose:** The build VM's network: what its `wb-netd` serves, the
one connection to the guest's port 22, and its refusal for session
VMs.

## Design

### The build VM's network

The image build's one SSH session needs a network
(S06-02-image-build, "Provisioning stages"). The build VM's `wb-netd`
gives it the least that session needs:

- DHCP and ARP, as for any VM (S07-02-packet-path, "Addressing");
- one TCP connection from its stack to the guest's port 22, on
  `wb-hostd`'s request;
- no egress. It resets every TCP connection the guest opens, drops
  all UDP, DNS included, and hands no stream to `wb-proxyd`.

`wb-netd` learns at start, with its VM and generation, whether its VM
is the build VM. On the request it:

1. refuses when its VM isn't the build VM, or when it has already
   opened the connection;
2. opens the connection to the guest's leased address, port 22, from
   the gateway address;
3. relays it over a Unix domain `SOCK_STREAM` socketpair, as for a
   stream (S07-05-stream-hand-off);
4. sends the other end to `wb-hostd` on its control channel, in a
   message with exactly one descriptor.

`wb-hostd` hands that end to `wb-build-ssh` (S04-05-helper-programs).
`wb-netd` doesn't open an upstream connection on this path.

A session VM's `wb-netd` refuses the request, and so does a build VM's
that has already opened its connection. Each refusal is logged with the
rule `build-connect-refused` and the check that failed. That refusal
guards against a confused request. A compromised `wb-hostd` that sends
one to a session VM still has no password for `wbadmin`, which only
`wb-guestadmin` holds (S15-04-guest-admin), and the image build turns
Remote Login off before it seals the image (S06-02-image-build).

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
