# X03 - Network path

**Purpose:** Whether a macOS guest works on the file-handle network attachment with gVisor's userspace stack on the host: a DHCP lease, only allowlisted names resolving, only the proxy reachable, no entitlement beyond virtualization, and how fast.

Brief: B17-network-path

## For review

- **Decides:** the answer to X03-network-path (I17), and how
  `wb-netd`'s packet path works, which S07-egress-gateway "Packet path"
  now states: a link filter in front of gVisor's stack, the DHCP
  options the gateway offers and never offers, local answers for
  reverse and service-discovery names, one approval event per name, the
  synthetic address pool, per-VM caps, rate limits on every event the
  guest can trigger, and the same MTU on both ends of the link. Empty
  answers to `HTTPS` and `SVCB` queries are the builder's choice, not
  measured (condition 3).
- **You are approving:** the answer "yes, with conditions", the
  measurements, and the spec text in this pull request. A 1 GiB
  download from the `wb-proxyd` stand-in reached about 350 MB/s at MTU
  1500 and about 570 MB/s at MTU 9000 or 65535. An upload reached
  about 300 MB/s and 950 MB/s.
- **Controls touched:** none weakened. SEC05-default-deny needs the
  link filter (condition 1): to accept connections to the synthetic
  addresses, gVisor's stack answers ARP for every address, and with
  that the guest refused every lease as an address conflict.
- **Assumed:** that `wb-proxyd`, which terminates and re-originates
  TLS, costs more than the stack, so the stack isn't the limit
  (X04-tls-inspection). That a guest with a user logged in and tools
  installed adds names to the idle list, but no new kind of traffic.
- **Open decisions:**
  1. The MTU of the guest link. Recommended: 65535, with `wb-netd`'s
     frame queues bounded in bytes. S07-egress-gateway says 1500 until
     this is decided.
  2. Approval prompts for the guest OS's own lookups: an idle guest
     looked up 14 host names on its own in its first hour, each one an
     approval event under S07-egress-gateway today. Recommended: refuse
     a list of OS background names without a prompt, with an audit
     entry, and turn those services off in the image
     (X17-image-build). The list is host-held policy of exact names,
     not suffixes, shown and counted in `wb status`, and the guest can
     never extend it. This amends FR09-approve-unknown.
     S07-egress-gateway keeps one approval event per name until this is
     decided.
- **Brief:** B17-network-path

## Question

From X00-index: a macOS guest boots on the file-handle attachment with
the gVisor stack, gets a DHCP lease, resolves only allowlisted names,
and reaches only the proxy. Confirm no entitlement beyond
virtualization is needed. Measure throughput. The issue (I17) adds:
lease, resolution and refusal behavior, including IPv6 and UDP other
than DNS, a 1 GB download at MTU 1500 and at the largest MTU the
attachment accepts, against the host's own throughput, every name an
idle macOS guest resolves in its first hour, and what Claude Code
resolves on start.

## Answer

**Yes, with conditions.**

1. **The guest boots on the file-handle attachment alone and gets a
   lease: yes.** Every boot had one NIC, the file-handle attachment on
   a `SOCK_DGRAM` socket pair, with no NAT network and no shared
   directory, the provisioning boot included. The guest's DHCP client
   had its ACK 4.7 to 5.7 s after the VM started, median 4.9 s (11
   cold boots). It
   took the address, router, DNS server and interface MTU (option 26)
   from the gateway, accepted broadcast replies, and renewed by unicast
   at half the lease.
2. **Only allowlisted names resolve: yes.** `A` queries for allowlisted
   names got a synthetic address from 198.18.0.0/15, the same one on
   every query. `AAAA` got an empty answer, other types were refused,
   and every other name got `NXDOMAIN`, over UDP and TCP. The system
   resolver agreed. DNS to any other server timed out over UDP and was
   reset over TCP.
3. **Only the proxy is reachable: yes, with the link filter.** TCP to a
   synthetic address on an allowed port reached the stub. Every other
   TCP connection was reset: a synthetic address on another port, an
   unallocated synthetic address, a public address and the host's LAN
   address within 1 ms. The gateway's port 80 failed after 1 s: the
   log shows the guest sending the SYN twice, so the stand-in lost the
   first reset or never sent it, and S07-egress-gateway requires
   `wb-netd` to reset at once. UDP other than DNS, multicast, broadcast
   other than DHCP and every IPv6 frame were dropped, and ping got an
   answer only from the gateway (`results/run1-mtu1500-probe.txt`).
   The probe's ping to the IPv6 all-nodes address `ff02::1` got 2 of 2
   replies, but those came from the guest itself: a multicast ping is
   looped back to the sender, and `wb-netd` dropped the IPv6 frames.
4. **No entitlement beyond virtualization: yes.** The `wb-vmd` stand-in
   was signed ad hoc with only `com.apple.security.virtualization`, and
   the `wb-netd` stand-in is a plain Go binary with no entitlements
   (`results/entitlements.txt`). Neither ran in the App Sandbox. The
   attachment accepts any MTU from 1500 to 65535, and the guest used
   the MTU the DHCP server offered, 9000 and 65535 included.
5. **Throughput: about 350 MB/s at MTU 1500, 570 MB/s at 9000 and
   65535.** That is a 1 GiB file from the stub over its Unix socket,
   through the stack, to `curl` in the guest. The stub is the limit at
   the larger MTUs: the host itself read the file from the stub's Unix
   socket at 637 MB/s. Served from the stack's own memory, the guest
   received about 510 MB/s at MTU 1500, 2,500 MB/s at 9000 and 3,300
   MB/s at 65535.

The conditions:

1. **A link filter in front of the stack.** To accept TCP to any
   synthetic address, the stack runs in promiscuous mode, and to answer
   from those addresses it runs with spoofing on. With spoofing on,
   gVisor answers an ARP request for any address (`pkg/tcpip/stack/nic.go`,
   `CheckLocalAddress`). The guest sends ARP probes for its own leased
   address after each ACK, and with the ARP rule off it got an answer,
   declined the lease (`DHCPDECLINE`), and tried again every 12 s
   without ever getting an address (`results/run11-*`). So `wb-netd`
   checks each frame before the stack sees it. The spike's filter
   checked the source MAC, IPv4 and ARP only, the leased source address
   (or `0.0.0.0` for DHCP), ARP requests only for the gateway address,
   ICMP only to the gateway, broadcast only to the DHCP server, no
   multicast, and UDP only to the gateway's port 53 and the DHCP
   server. Review of this result found it too loose in places the
   spike didn't exercise: it passed ARP replies with any sender
   hardware address, frames to a foreign destination MAC, IPv4 options
   and fragments, and every ICMP type to the gateway. S07-egress-gateway
   states the complete filter, which closes those. With the ICMP rule
   off, gVisor still didn't answer ping to other addresses
   (`results/run12-*`), so that rule is a second layer.
2. **The gateway offers only what it needs.** The macOS client asks
   for options the gateway never offers: WPAD (252, a proxy
   configuration URL, FR08-no-proxy-config), classless static routes
   (121), IPv6-only preferred (108), encrypted DNS (162), NetBIOS,
   LDAP and a domain search list. The gateway offers the subnet mask,
   router, DNS server, lease time, server identifier and interface
   MTU.
3. **`HTTPS` and `SVCB` queries get an empty answer (not measured).**
   The macOS resolver asks for an `HTTPS` record before most `A`
   queries, and asks `_dns.resolver.arpa` for `SVCB` (discovery of an
   encrypted DNS server) after each lease. The spike refused `HTTPS`
   for allowlisted names, and the connection followed in the same
   second, so refusing broke nothing measured. The spec gives an empty
   answer instead. That is the builder's choice, not a measurement: an
   empty answer says "no such record" without signaling an error, and
   no `HTTPS` record can then carry an encrypted ClientHello
   configuration or an HTTP/3 hint that `wb-proxyd` can't honor. The
   cost: nobody has checked the macOS resolver behaves the same
   with empty answers, which S11-verification-and-spikes now checks.
   A resolver fallback is no reason for it here: every other DNS
   server is dropped.
4. **The MTU is the same on both ends.** `wb-vmd` sets the
   attachment's `maximumTransmissionUnit`, and the DHCP server offers
   the same value as option 26. Apple's header for the attachment asks
   for an `SO_RCVBUF` of at least twice the `SO_SNDBUF` on the host
   end, four times for the best throughput. The spike used 1 MiB and 4
   MiB.
5. **UDP other than DNS is dropped, NTP included.** The guest looked up
   `time.apple.com` and sent NTP every 2 s, and all of it was dropped.
   So the guest clock comes only from `wb-guestd`. S06-vm-lifecycle has
   `wb-guestd` set it after a restore or host sleep, but not after a
   cold boot (I93).

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), Go 1.27.1, gVisor
`go` branch at `v0.0.0-20261004063249-f57b8fc79db4`. Guest: macOS
27.0.1, 4 vCPU, 4 GiB, an APFS clone of the installed bundle that the
X18-vsock-handoff spike made (its result is PR87, pending), provisioned
by this spike on the file-handle attachment. The spike ran commands in the guest
over SSH through a forward in the `wb-netd` stand-in, from host
127.0.0.1:2222 through the stack to the guest's port 22. It was on in
every run except the idle hour (run6). The product has no such path
(S07-egress-gateway). Each row is three transfers of 1 GiB with `curl` in the
guest, one boot per row group, in MB/s (10<sup>6</sup> bytes per
second): the median, and the range. CPU is CPU-seconds per GiB, for the
`wb-netd` stand-in (which also ran the stub) and for Virtualization's
service process, which includes the guest's own CPUs.

| Transfer | MTU | MB/s, median (range) | `wb-netd` CPU s | Virtualization CPU s |
|---|---|---|---|---|
| Download from the stub | 1500 | 351 (345 to 356) | 7.3 | 11.8 |
| Download from the stub | 9000 | 567 (559 to 570) | 4.3 | 7.4 |
| Download from the stub | 65535 | 566 (540 to 576) | 4.2 | 7.4 |
| Download from memory | 1500 | 506 (500 to 525) | 4.1 | 8.2 |
| Download from memory | 9000 | 2,522 (2,363 to 2,688) | 0.9 | 2.2 to 3.1 |
| Download from memory | 65535 | 3,315 (2,816 to 3,573) | 0.7 | 1.7 to 2.6 |
| Upload to the stub | 1500 | 303 (282 to 310) | 10.5 | 13.4 |
| Upload to the stub | 65535 | 953 (935 to 953) | 2.5 | 4.3 |

- **Host baseline.** The host read the same file from the stub at
  6,558 to 6,978 MB/s over loopback TCP, where the stub uses
  `sendfile`, and at 635 to 639 MB/s over the stub's Unix socket, the
  path the guest's streams take (`results/host-baseline.txt`). At MTU
  9000 and above, the guest reached about 89% of the Unix socket
  figure. No run compared a download from the internet, which the
  guest can't reach.
- **Where the time goes.** At MTU 1500 a GiB takes at least 740,000
  frames of 1,448 bytes of TCP payload, one datagram each, so the cost
  per frame dominates. At 65535 it takes about 16,400.
  A 1 MiB relay buffer instead of `io.Copy`'s 32 KiB changed downloads
  by less than 2% and uploads by 5% at MTU 1500 and 9% at 65535
  (`results/run8-*`, `results/run9-*`).
- **Lease.** NIC hand-off to the first DHCP ACK on 11 cold boots
  (runs 1 to 10 and 12): 4.69 to 5.67 s, median 4.90 s. The
  provisioning boot took 8.13 s. Run 11, with the ARP rule off, had
  its first ACK at 4.82 s and then declined it. X02-warm-start measured the guest answering on the
  network 5.04 s (p50) after a cold boot.
- **Other runs.** The probe at MTU 1500 measured 360, 348 and 367 MB/s,
  and a second boot at MTU 65535 measured 544, 571 and 577 MB/s.

## What the guest resolves

**An idle guest, first hour.** A fresh clone at the login window, with
no user logged in and nothing allowlisted, sent 109 queries for 16
names in its first hour (`results/run6-idle-hour-netd.jsonl`). All got
`NXDOMAIN`. Under S07-egress-gateway as it stood, each of the 16 names
would have raised approval events: the 14 host names, the reverse
lookup of the guest's own address and `_dns.resolver.arpa`. The spec now
answers the last two locally, with no approval event. Most names came
in the first 10 s, and six repeated all hour, the first six rows:

| Name | Queries | Types |
|---|---|---|
| `init.push.apple.com` | 46 | `A`, `HTTPS` |
| `push.apple.com` | 23 | `TXT` |
| `albert.apple.com` | 12 | `A`, `HTTPS` |
| `configuration-carry.ls.apple.com` | 6 | `A`, `HTTPS` |
| `15.2.0.10.in-addr.arpa` (its own address) | 3 | `PTR` |
| `_dns.resolver.arpa` | 3 | `SVCB` |
| `gateway.icloud.com`, `gdmf.apple.com`, `gspe1-ssl.ls.apple.com`, `gspe35-ssl.ls.apple.com`, `mesu.apple.com`, `xp.apple.com` | 2 each | `A`, `HTTPS` |
| `1-courier.push.apple.com`, `1-courier.sandbox.push.apple.com`, `appleid.apple.com`, `time.apple.com` | 1 each | `A` |

With every name resolving (`results/run5-mtu1500-learn-netd.jsonl`), a
boot of the same guest opened TCP connections to port 443 of
`albert.apple.com`, `init.push.apple.com`, `gdmf.apple.com`,
`mesu.apple.com`, `gspe1-ssl.ls.apple.com`, `gspe35-ssl.ls.apple.com`,
`configuration-carry.ls.apple.com`, `gateway.icloud.com` and
`xp.apple.com`, and sent NTP to the synthetic address of
`time.apple.com`.

**Claude Code on start.** Claude Code 2.1.289, copied into the guest
with a placeholder API key and an empty home directory, looked up
`api.anthropic.com` (`A` and `AAAA`) for `claude -p`, and also
`raw.githubusercontent.com` and `platform.claude.com` when started
interactively. Starting the new binary also looked up
`ocsp2.apple.com`, `gateway.icloud.com` and `api.apple-cloudkit.com`,
which looks like the guest checking the binary. With every name
resolving, `claude -p` retried `api.anthropic.com` for its whole 60 s
and contacted nothing else, because the stub doesn't speak TLS. Names
that Claude Code contacts after a successful API call are not in this
list (X01-model-credential, X04-tls-inspection).

## What was not measured

- **TLS.** The stub served plain HTTP, so the numbers are the stack and
  the relay, not `wb-proxyd` with inspection (X04-tls-inspection).
- **A real upstream.** Nothing left the host.
- **Restore.** Every boot was a cold boot. The X18-vsock-handoff result
  (PR87, pending) reports the network descriptor working after a
  restore, with a new socket pair.
- **Empty `HTTPS` and `SVCB` answers.** The spike refused them
  (condition 3).
- **Fragments, IPv4 options, foreign destination MACs, forged ARP
  replies.** The guest sent none, and the spike's filter didn't check
  them (condition 1).
- **App Sandbox.** Neither process ran in the App Sandbox.
  X25-vmd-sandbox decides how `wb-vmd` is confined.
- **More than one VM.** One VM at a time, on a host doing nothing
  else.
- **Upload at MTU 9000.** Only 1500 and 65535.
- **A working guest.** The idle list comes from a guest at the login
  window with nothing installed. No run measured the names a logged-in
  user, Xcode or Homebrew add.

## What it means for the specs

- **S07-egress-gateway**, "Packet path": the complete link filter,
  the DHCP options and message types, the MTU on both ends, local
  answers for reverse and service-discovery names, one approval event
  per name, the synthetic address pool, per-VM caps, rate limits on
  every event class the guest can trigger, the empty answers to
  `HTTPS` and `SVCB`, NTP, no host-side listener in `wb-netd`, and the
  measured throughput under "Performance" (changed in this pull
  request).
- **S11-verification-and-spikes**: the link filter and its ARP branch
  in the fuzz list, conformance checks that nothing answers the
  guest's ARP probes or a ping to anything but the gateway, that the
  lease holds only the listed options, and that the macOS resolver
  behaves the same with empty `HTTPS` answers. The download benchmark
  records CPU time and compares with this spike (changed in this pull
  request).
- **FR09-approve-unknown**: unchanged in this pull request. Option A of
  open decision 2 would amend it, because names on the list would be
  refused without an approval.
- **S12-platforms**: unchanged. Its macOS packet transport row is
  right, and I29's pull request (PR87) rewrites that row.
- **S06-vm-lifecycle**: setting the guest clock after a cold boot
  (condition 5) is I93, because PR87 changes that section.
- **S10-tech-stack**: unchanged. It already gives the virtualization
  entitlement to `wb-vmd` only.

## Spike code

Branch `spike/x3-network-path`, at
[4aaa7fe](https://github.com/wraithbox/wraithbox/tree/4aaa7fe0ce30c8164e74496fd3175fd3b60af099/spikes/x03-network-path):
the Swift `wb-vmd` stand-in, the Go `wb-netd` stand-in with its stub,
the in-guest probe, the scripts, and the raw logs in `results/`.

**Status:** Answered 2026-10-05: yes, with conditions
