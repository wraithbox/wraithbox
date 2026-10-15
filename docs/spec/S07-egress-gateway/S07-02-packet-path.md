# S07-02 - Packet Path

**Purpose:** How `wb-netd` carries the guest's frames: the packet transport, the
stack, the link filter, its caps, addressing, the MTU and connections.

## Design

### Packet transport

Each VM's virtual NIC is connected to its
`wb-netd` by a platform-specific packet transport (S12-platforms) that
needs no host privileges and no host network device. On macOS,
`wb-vmd` creates a datagram socketpair per VM, gives one end to the
VM's network device (file-handle attachment) and passes the other to
that VM's `wb-netd` through `wb-hostd` (`SCM_RIGHTS`). Every Ethernet
frame the guest sends arrives in `wb-netd`; the guest has no other
network path. A NAT or bridged network device would be one, and
`wb-vmd`'s device-set check keeps it out, not its sandbox profile
(S04-02-vm-provider, "The device set is fixed and checked",
X25-vmd-sandbox). The rest of this spec is the same on every platform.

### Stack

gVisor's userspace TCP/IP stack (`pkg/tcpip`), consumed as a
Go module from gVisor's `go` branch. Its file-descriptor link endpoint
is Linux-only, so Wraith Box provides a small link endpoint that moves
frames between the packet transport and the stack, with one transport
adapter per platform. On macOS each datagram is one Ethernet frame,
and the host end's `SO_RCVBUF` is at least twice its `SO_SNDBUF`, as
Apple's header for the attachment asks. The adapter reads each
datagram into a buffer of MTU + 15 bytes, one more than the largest
frame, so a datagram that fills the buffer was truncated. It is
dropped and logged with the rule `frame-truncated`, never passed on
in part. The stack runs in promiscuous
and spoofing mode, so that it accepts and answers connections to
every synthetic address (X03-network-path).

### Link filter

With spoofing on, gVisor answers ARP for any
address, and the guest then declines every lease as an address
conflict (X03-network-path). So `wb-netd` checks every frame before
the stack sees it, and drops it unless all of these hold, in this
order:
- The frame is 14 to MTU + 14 bytes long.
- Its source MAC is the guest's, and its destination MAC is the
  gateway's or broadcast.
- It is ARP or IPv4. Every other EtherType is dropped, IPv6
  included.
- ARP is Ethernet/IPv4 (hardware type 1, protocol type `0x0800`,
  address lengths 6 and 4), with the operation request or reply, the
  sender hardware address equal to the guest's MAC, the sender
  protocol address equal to the leased address, and the target
  protocol address equal to the gateway. So the guest's
  probes for its own address go unanswered, because an answer would
  tell it the address is taken, and nothing the guest sends can
  claim another address in the stack's neighbor table.
- IPv4 has a header of 20 bytes (no options) and is not a fragment
  (MF clear, offset 0). Its source is the leased address, or
  `0.0.0.0` only for DHCP from UDP port 68 to port 67.
- TCP passes to the stack, which decides under "Connections". UDP
  passes only to the gateway's port 53, or from port 68 to port 67
  for DHCP, to the gateway or to limited and subnet-directed broadcast. ICMP passes only as an
  echo request to the gateway. Every other IP protocol, multicast,
  and broadcast other than DHCP is dropped. gVisor didn't answer ping
  to other addresses even without the ICMP rule, which is a second
  layer.

### Events and rate limits

Each drop is counted and logged with its
rule. Every class of event the guest can trigger is rate-limited per
rule: link filter drops, DHCP findings, DNS refusals, queries
dropped with `dns-hold-full`, approval events, `handoff-send-failed`, and in `wb-proxyd` stream resets,
HTTP refusals (S07-10-non-http-streams) and the hand-off refusals
`handoff-socket-refused`, `name-report-refused` and
`stream-handoff-refused`, including the `stream-cap` check (S07-04-stream-hand-off). The record that follows a suppressed run holds the count
it suppressed (SEC10-audit, SEC13-bounded-resources). A string from
the guest that goes into a log record, such as a DHCP option, a DNS
name, or a name or label in a hand-off message, is escaped and cut
to a fixed length in code, and a cut string ends with a truncation
marker. Packets the stack sends itself, such as
its own broadcast DHCP replies, are never counted as guest drops.

### Per-VM caps

Each VM's `wb-netd` caps TCP connections in flight
(half-open and established), DNS-over-TCP connections, held DNS
queries (S07-03-dns, "Hold limits"), the stack's buffer memory, and the bytes in its frame queues, not only their
frame counts. Each cap is configuration with a default. A frame or
connection over a cap is dropped and logged with the cap's rule
(SEC13-bounded-resources).

### No host-side listener

`wb-netd` listens on nothing on the host
and never opens a connection to the guest. Its only peers are the
packet transport, `wb-proxyd`, and `wb-hostd` over the control,
accounting and event channels it inherits at start
(S04-07-supervision). A path from the host into the
guest, such as port forwarding (FR11-port-forward), needs its own
spec text, and doesn't go through `wb-netd`.

### Addressing

`wb-netd` runs a DHCP server handing the guest a single
private IPv4 address, with the gateway as router and DNS server. Only
the guest's own MAC and leased address are accepted. IPv6 is not
offered and all IPv6 frames are dropped. A reply holds only the
subnet mask, router, DNS server, lease time, server identifier and
interface MTU. The macOS client also asks for a proxy configuration
(WPAD, option 252), static routes, encrypted DNS, IPv6-only
preferred, NetBIOS, LDAP and a search domain, and the gateway never
offers them (FR08-no-proxy-config). The server answers DISCOVER with
OFFER, REQUEST for the leased address with ACK and any other REQUEST
with NAK, and INFORM with an ACK of the same options and no address.
RELEASE is logged, and the lease stays with the guest's MAC, because
there is only one. A `DHCPDECLINE` means something answered for the
guest's address, and is logged as a finding. Strings the guest
sends, such as the host name, vendor class and client identifier
(options 12, 60, 61 and 81), are logged escaped and cut to the
fixed length ("Events and rate limits"), never raw.

### MTU

The link MTU is 65535, the largest the macOS attachment
accepts (1500 to 65535). `wb-vmd` sets it on the attachment, the
DHCP interface-MTU option (26) offers the same value, and the guest
uses the offered value. The maintainer decided this on I17
(B17-network-path). `wb-netd` isn't a router: the guest's TCP
connections end in its stack, and `wb-proxyd` opens its own
connections upstream at the host's MTU, so the link MTU never
reaches another network. The largest frame is then 65535 + 14
bytes. `wb-netd` bounds its frame queues in bytes as well as in
frames ("Per-VM caps", SEC13-bounded-resources), and the link
endpoint's fuzz target covers frames of that size
(C01-network-and-dns).

DNS is on its own page, S07-03-dns.

### Connections

- TCP to a synthetic address on an allowed port is accepted by the
  stack and handed to `wb-proxyd` as a byte stream over the VM's
  hand-off socket, with the synthetic address and port (S07-04-stream-hand-off). Once X14-flow-attribution delivers
  labels, the hand-off also holds the label the guest reported for
  the flow, marked untrusted (S13-guest-confinement). The project and
  session come only from that label, and it can only narrow.
- A port is allowed for a synthetic address when the effective
  policy names it for that address's host: in an endpoint's `port`
  or `ports`, or in a raw TCP entry (S07-10-non-http-streams). TCP to a
  synthetic address on any other port is reset at the first SYN and
  audited with the rule `port-not-allowed`.
- TCP to any other address is reset; UDP other than DNS is dropped
  (clients fall back from QUIC to TCP); ICMP is answered only for the
  gateway address. The host, the LAN, and raw IP destinations are
  therefore unreachable by construction (SEC05-default-deny).
  TCP to the gateway on any port but 53 is reset too. The reset is
  sent for the first SYN: in the spike the guest sent the SYN to the
  gateway's port 80 twice before it failed (X03-network-path).
- NTP is UDP, so it is dropped, and the guest clock comes only from
  `wb-guestd` (S06-vm-lifecycle).

### Performance

Packets are processed in userspace, so throughput is lower than kernel
networking. S11-verification-and-spikes sets a benchmark for large downloads. If throughput
misses it, the fix is inside `wb-netd` (batching, buffer sizes), not a
second network path.

X03-network-path measured a 1 GiB download from a plain HTTP stand-in
for `wb-proxyd` at about 350 MB/s at MTU 1500 and 570 MB/s at MTU 9000
or 65535, where the stand-in was the limit. It measured uploads only at
MTU 1500 and 65535, at about 300 and 950 MB/s. At MTU 1500 the cost
per frame dominates: the `wb-netd` stand-in used about 7 CPU-seconds
per GiB downloaded, and about 4 at the larger MTUs. Those CPU figures
include the stand-in for `wb-proxyd`, which ran in the same process.
On those figures the link uses MTU 65535 ("MTU"), and
`wb-netd` bounds its frame queues in bytes, not only in frames
(SEC13-bounded-resources).

**Status:** Draft
