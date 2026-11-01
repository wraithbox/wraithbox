# C01 - Network and DNS

**Purpose:** The test cases for the guest's network path: the link
endpoint, addressing, and the DNS gateway with its hold, quiet
refusals, and negative answers.

**Verifies:** S07-02-packet-path ("Addressing"), S07-18-build-network, S07-03-dns ("Name form", "Negative answers"),
S07-04-dns-refusals ("Hold", "Widening during the hold"),
S04-02-vm-provider ("The device set is fixed and checked")

## Fuzzing

- Ethernet, IP and TCP handling at the link endpoint, and DHCP.
  `FuzzLinkEndpoint` (`internal/netd/stack`) feeds frames through the
  link endpoint into the stack with the TCP rules and no hooks. The
  stack must send frames only to the guest's MAC or broadcast, answer
  ARP only for the gateway, and send no SYN-ACK.
- The link filter of S07-egress-gateway and its ARP branch, with a
  corpus of frames from 0 to 65535 + 14 bytes, and seeds of truncated
  reads that fill the MTU + 15 byte buffer, which must be dropped.
  `FuzzLinkFilter` (`internal/netd/link`) checks that the filter agrees
  with a second check written with gVisor's header package on every
  frame, and never passes one longer than MTU + 14 bytes.
- DNS, with a corpus that holds a dot byte inside a wire label, a label
  of 0 and of 64 bytes, and a name of 254 bytes (S07-03-dns, "Name form").

## Conformance

### Interfaces and the link

- List the guest's network interfaces and routes, and see exactly one
  Ethernet interface, its lease from `wb-netd`, and no address or
  route in a vmnet subnet, which a NAT device would add
  (X25-vmd-sandbox).
- Connect to a raw IP address, to the host, to the LAN, and over IPv6.
  Ping them and a synthetic address, and see that nothing answers.
- See that nothing answers the guest's ARP probes for its own address,
  and that the lease holds only the options in S07-egress-gateway's
  list (X03-network-path).
- Send a forged ARP reply, a fragment, an IPv4 packet with options, and
  a frame to another destination MAC, and see each dropped with its
  rule.

### Lookups

- With the gateway's empty answers to `HTTPS` and `SVCB`, see the macOS
  resolver still connect to an allowlisted name as fast as it did when
  they were refused (B17-network-path).
- Resolve a name on the guest OS's background list, and see `NXDOMAIN`,
  an audit entry and no approval event, also when it is sent in mixed
  case and with a trailing dot. Resolve a name one label longer under
  it, and see an approval event.
- See the same answer bytes for a name that isn't allowlisted, one that
  is denied, one on the background list, and one in a part that is off
  (S07-04-dns-refusals, "Quiet refusals").
- Send UDP other than DNS, resolve a name that isn't allowlisted,
  exceed the wildcard budget, and use a DNS server other than the
  gateway.

### Negative answers and approvals during a lookup

- Resolve a name that isn't allowlisted, approve it within 4 seconds,
  and see the same lookup answered with an address.
- Resolve another, leave it unanswered, and see `NXDOMAIN` after 4
  seconds with an SOA record of TTL 0 and `MINIMUM` 0. Then approve it,
  resolve it again within 1 second of the approval, and see it succeed,
  so the guest didn't cache the refusal. A guest resolver that keeps a
  minimum negative cache time despite the zero TTL fails this check,
  and the delay it adds after a late approval is then written into
  S07-03-dns, "Negative answers".
- See that an empty `AAAA` answer for an allowed name has the TTL of
  its `A` answer.

### Hold

- Time the refusals of a denied name, a name on the background list, a
  name that isn't allowlisted, and a name refused with `audit-budget`,
  once with the accounting channel answering at once and once with it
  blocking for just under its 100 ms limit. See the same hold for each.
- See the guest's resolver (`mDNSResponder` on macOS) wait for a held
  answer and not fail the lookup sooner (S07-04-dns-refusals, "Hold",
  B51-approval-flow).
- With the VM over its audit budget, resolve a name that isn't
  allowlisted, approve it during the hold, and see the lookup still
  refused at the end of the hold with `audit-budget`
  (S07-04-dns-refusals, "Widening during the hold").
- Send more refused queries at once than the hold limit, and see the
  rest dropped and counted with `dns-hold-full`, with no approval event
  for them. Send one over DNS over TCP, and see the connection closed.
- With queries held, approve a name, so that more held queries are run
  again at once than the query rate allows. See the excess refused with
  `dns-query-rate` at the end of their holds, with no new requests,
  learn entries or lookup counts.
- Close a DNS-over-TCP connection for `dns-hold-full` while it has held
  queries, and see their timers not fire (S07-04-dns-refusals, "Hold").

### The build VM's network

- Start `wb-netd` with no mode, and with an unknown one, and ask each
  for the connection to the guest's port 22. See each refused and
  logged with `build-connect-refused` and the check, with no SYN sent
  to the guest.
- Send a build VM's `wb-netd` a control message that names a mode, and
  see it refused with `build-mode-refused` and the mode unchanged.
- Ask the build VM's `wb-netd` with another VM's name, with an older
  generation, and twice at once, and see each refused with
  `build-connect-refused`. After it has handed a socket, ask again and
  see that refused too.
- With nothing listening on the guest's port 22 at first, see
  `wb-netd` try again, then hand one socket and log `build-connect`
  once `sshd` answers. With nothing listening at all, see
  `build-connect-timeout` at the deadline and no socket.
- From the build guest, open TCP to a raw IP address, the gateway and
  a synthetic address, and send a DNS query. See each reset or
  dropped and logged with `build-egress-refused`, and no stream handed
  to `wb-proxyd`.

**Status:** Draft
