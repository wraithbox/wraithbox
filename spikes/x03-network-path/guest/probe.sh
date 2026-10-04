#!/bin/sh
# X03-network-path spike: runs in the guest (as the provisioned user, over
# gssh.sh) and tries each path S07-egress-gateway says must work or fail.
# Each step prints "== <name>" and its output; x03-netd logs its side.
# Usage: probe.sh <host LAN IPv4> [bulk-runs]
lan=${1:-192.168.1.1}
runs=${2:-3}
step() { echo "== $1"; }
t() { /usr/bin/time -p "$@" 2>&1 | grep -v '^user\|^sys'; }

step "ifconfig en0"; ifconfig en0 | grep -E 'mtu|inet'
step "lease"; ipconfig getpacket en0 | grep -E 'yiaddr|router|domain_name_server|interface_mtu|lease_time'

step "dns A allowlisted"; dig +time=2 +tries=1 @10.0.2.2 bulk.test A | grep -E 'status|^bulk'
step "dns A allowlisted again (same synthetic address)"; dig +short +time=2 +tries=1 @10.0.2.2 bulk.test A
step "dns A second name"; dig +short +time=2 +tries=1 @10.0.2.2 allowed.test A
step "dns AAAA allowlisted (empty)"; dig +time=2 +tries=1 @10.0.2.2 bulk.test AAAA | grep -E 'status|ANSWER:'
step "dns MX allowlisted (refused)"; dig +time=2 +tries=1 @10.0.2.2 bulk.test MX | grep -E 'status'
step "dns A not allowlisted (NXDOMAIN)"; dig +time=2 +tries=1 @10.0.2.2 example.com A | grep -E 'status'
step "dns over TCP"; dig +tcp +time=2 +tries=1 @10.0.2.2 bulk.test A | grep -E 'status|^bulk'
step "dns to another server (dropped, times out)"; t dig +time=2 +tries=1 @8.8.8.8 example.com A | grep -E 'status|timed out|real'
step "dns to another server over TCP (reset)"; t dig +tcp +time=2 +tries=1 @8.8.8.8 example.com A | grep -E 'status|refused|reset|timed out|real'
step "system resolver"; dscacheutil -q host -a name bulk.test; dscacheutil -q host -a name example.com; echo "(empty = not found)"

step "tcp allowed: synthetic, port 80"; curl -sS -m 5 http://bulk.test/hello
step "tcp synthetic, port not allowed (reset)"; t curl -sS -m 5 http://bulk.test:8080/ 2>&1 | grep -E 'curl|real'
step "tcp synthetic, unallocated (reset)"; t curl -sS -m 5 http://198.18.200.200/ 2>&1 | grep -E 'curl|real'
step "tcp raw public IP (reset)"; t curl -sS -m 5 http://1.1.1.1/ 2>&1 | grep -E 'curl|real'
step "tcp gateway (reset)"; t curl -sS -m 5 http://10.0.2.2/ 2>&1 | grep -E 'curl|real'
step "tcp host LAN address $lan (reset)"; t curl -sS -m 5 "http://$lan/" 2>&1 | grep -E 'curl|real'
step "tcp host loopback via 127.0.0.1 is the guest's own"; curl -sS -m 3 http://127.0.0.1:18080/ 2>&1 | head -1
step "tcp ssh to raw IP port 22 (reset)"; nc -z -G 3 1.1.1.1 22; echo "nc exit $?"

step "icmp gateway"; ping -c 2 -t 3 10.0.2.2 | tail -2
step "icmp public IP (dropped)"; ping -c 2 -t 3 1.1.1.1 | tail -2
step "icmp synthetic (dropped)"; ping -c 2 -t 3 198.18.0.1 | tail -2
step "icmp host LAN (dropped)"; ping -c 2 -t 3 "$lan" | tail -2

step "udp QUIC to synthetic 443 (dropped)"; echo x | nc -u -w 1 198.18.0.1 443; echo "nc exit $?"
step "udp to public IP (dropped)"; echo x | nc -u -w 1 1.1.1.1 9999; echo "nc exit $?"
step "udp NTP (dropped)"; sntp -t 2 17.253.14.253 2>&1 | tail -1

step "ipv6 link-local all-nodes (dropped)"; ping6 -c 2 -i 1 ff02::1%en0 2>&1 | tail -2
step "ipv6 global (no route)"; curl -sS -m 3 -6 'http://[2606:4700:4700::1111]/' 2>&1 | head -1
step "ipv6 addresses"; ifconfig en0 | grep inet6

i=1
while [ "$i" -le "$runs" ]; do
  step "bulk download $i"
  curl -sS -o /dev/null -w 'bytes %{size_download} seconds %{time_total} Bps %{speed_download}\n' http://bulk.test/1g
  i=$((i + 1))
done
step "counters"; netstat -s -p tcp | grep -E 'retransmit|out-of-order|duplicate' | head -6
