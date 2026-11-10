# C01 - Network and DNS

**Purpose:** The test cases for the guest's network path: the link
endpoint, addressing, and the DNS gateway with its hold, quiet
refusals, and negative answers.

**Verifies:** S07-02-packet-path ("Addressing"), S07-18-build-network, S07-03-dns ("Messages", "Order of checks", "Name form", "Synthetic pool", "Local answers", "Negative answers", "Wildcard budget"),
S07-04-dns-refusals ("Hold", "Widening during the hold"),
S04-02-vm-provider ("The device set is fixed and checked")

## Fuzzing

- Ethernet, IP and TCP handling at the link endpoint, and DHCP.
  `FuzzLinkEndpoint` (`internal/netd/stack`) feeds frames through the
  link endpoint into the stack with the TCP rules and no hooks. The
  stack must send frames only to the guest's MAC or broadcast, answer
  ARP only for the gateway, and send no SYN-ACK. A DHCP reply goes to
  the guest's MAC, with the leased address or zero as `yiaddr`.
- The DHCP server. `FuzzDHCP` (`internal/netd/dhcp`) feeds messages
  through the parser, the reply and the server loop, with seeds from
  the macOS client's DISCOVER and REQUEST. The parser must fail only
  with a form error, which the server drops with `dhcp-form`. A reply
  must go only to the
  guest's MAC, hold the leased address or zero as `yiaddr`, and hold
  only the message type and the six options of S07-02-packet-path,
  "Addressing". Every string in a log record must be printable and cut
  to its length.
- The link filter of S07-egress-gateway and its ARP branch, with a
  corpus of frames from 0 to 65535 + 14 bytes, and seeds of truncated
  reads that fill the MTU + 15 byte buffer, which must be dropped.
  `FuzzLinkFilter` (`internal/netd/link`) checks that the filter agrees
  with a second check written with gVisor's header package on every
  frame, and never passes one longer than MTU + 14 bytes.
- The DNS name form, with a corpus that holds a dot byte inside a wire
  label, a label of 0 and of 64 bytes, and a name of 254 bytes
  (S07-03-dns, "Name form"). `FuzzNameForm` (`internal/dnsname`) feeds
  lists of labels of 0 to 255 bytes. An accepted name must be in
  canonical form and pass the check again unchanged as a host. The wire
  and host checks must agree on every list a host can hold, and a
  refusal message must be printable ASCII.
- The synthetic pool (S07-03-dns, "Synthetic pool"). `FuzzPoolOps`
  (`internal/netd/synth`) runs a sequence of answers for names the
  fuzzer picks, connection opens and closes, clock steps, sweeps, and
  failing writes and sends, on a pool of 8 addresses. Each address
  maps to one name at a time, and goes to another name only after its
  release was recorded and reported. The pool never releases a pinned
  entry, and holds at most 8 live entries.
- The DNS server (S07-03-dns, "Messages", "Order of checks").
  `FuzzDNSQuery` (`internal/netd/dns`) feeds message bytes through the
  parser, the decision and the hold, with a fake policy that allows one
  name. The corpus holds a dot byte inside a wire label, a label of 0
  and of 64 bytes, a name of 254 bytes and one over 255, a compression
  pointer in the question, and the queries an idle macOS guest sent in
  X03-network-path, with and without an OPT record. A message that
  fails the form gets no response. A response's question must be the
  query's wire bytes, an `A` record must be for the allowed name and in
  198.18.0.0/15, and every `NXDOMAIN` must hold the zero SOA. A held
  refusal and a local answer must be the same bytes for one query
  whatever the rule.

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
- Send queries for a whole hold at many times the query rate, and see
  those over the rate dropped at once with `dns-query-rate`, no answer
  sent for them, and none dropped with `dns-hold-full`. See a name that
  isn't allowlisted, looked up during the flood, still raise an
  approval event once the rate has a token for it. Over DNS over TCP,
  send one over the rate on a connection with a held query, and see the
  connection stay open and the held query answered at the end of its
  hold. On a connection without one, see the connection closed.
- With queries held, approve a name, so that more held queries are run
  again at once than the query rate allows. See the excess refused with
  `dns-query-rate` at the end of their holds, with no new requests,
  learn entries or lookup counts.
- Close a DNS-over-TCP connection for `dns-hold-full` while it has held
  queries, and see their timers not fire (S07-04-dns-refusals, "Hold").

### Host-side DNS tests

The host-side parts of "Lookups", "Negative answers and approvals
during a lookup" and "Hold" run as Go tests in `internal/netd/dns`,
with fakes and `testing/synctest` as the clock:

- `TestAllowedA`, `TestOtherTypes` and `TestPoolFull`: the answers to
  an allowed name, and `REFUSED` at once for `TXT`.
- `TestLocalAnswers`: the local answers, at once with the zero SOA.
- `TestRefusalsAreAlike`: every refusal reason gives the same bytes,
  sent at 4 seconds and not before, also with each accounting write
  taking 99 ms.
- `TestBackground` and `TestBackgroundPastBudget`: quiet refusals in
  mixed case and past the budget, and an approval event for a name
  one label longer.
- `TestBudgetDebits`, `TestBudgetSpent` and `TestKeptBudget`: which
  lookups debit the wildcard budget, and what is refused once it is used up.
- `TestHoldFull` and `TestClose`: the hold limit, and no held refusal
  sent after the server closes.
- `TestFormFloodSpendsRate`: a name that fails the form spends a rate token.
- `TestRateDrops` and `TestRateFloodLeavesHoldRoom`: a query over the
  rate is dropped at once, logged and counted with `dns-query-rate`,
  and a flood over the rate leaves room in the held-query table for a
  name that raises an approval event, at the smallest cap.
- `TestHeldQueriesMinimum`: a held-query cap the rate could fill in one
  hold is refused.
- `TestParseQueryRefuses`: every `dns-message-form` case is dropped
  without an answer.

`TestDNSOverUDP` (`internal/netd/stack`) sends real UDP queries
through the stack: an allowed name resolves at the gateway, and a
query to another server is dropped with `udp-not-dns`.

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
