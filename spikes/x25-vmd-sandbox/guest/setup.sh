#!/bin/sh
# X18-vsock-handoff: put x18-guestd into a freshly installed guest.
# First boot with VZMacGuestProvisioningOptions (macOS 27 host and guest):
# a user and Remote Login, NAT network. Then copy the daemon in over SSH,
# install it as a root LaunchDaemon with sudo, and shut the guest down.
# Throwaway; the production path is X17-image-build.
# Usage: setup.sh <x18 binary> <bundle> <x18-guestd binary> <scratch dir>
set -eu
x18=$1 bundle=$2 guestd=$3 scratch=$4
here=$(cd "$(dirname "$0")" && pwd)
pass=$(openssl rand -hex 12)
printf '%s\n' "$pass" >"$scratch/guest-pass"
printf '#!/bin/sh\necho %s\n' "$pass" >"$scratch/askpass"
chmod 700 "$scratch/askpass"
mac=$(sed -E 's/.*"mac":"([^"]*)".*/\1/' "$bundle/cfg.json")
# dhcpd_leases writes the MAC without leading zeros per octet.
leasemac=$(echo "$mac" | awk -F: '{for(i=1;i<=NF;i++){sub(/^0/,"",$i); printf "%s%s",$i,(i<NF?":":"")}}')
echo "mac $mac lease $leasemac" >&2

"$x18" provision "$bundle" x18 "$pass" 40 >"$scratch/provision.jsonl" 2>"$scratch/provision.err" &
vmpid=$!
t0=$(date +%s)

ip=""
while [ -z "$ip" ]; do
  sleep 5
  kill -0 "$vmpid" 2>/dev/null || { echo "vm exited" >&2; exit 1; }
  ip=$(awk -v m="1,$leasemac" '/ip_address=/{split($0,a,"=");ipa=a[2]} /hw_address=/{split($0,b,"=");if(b[2]==m){print ipa}}' /var/db/dhcpd_leases 2>/dev/null | tail -1)
done
echo "guest ip $ip after $(( $(date +%s) - t0 ))s" >&2

export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
sshopts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no"
until ssh $sshopts "x18@$ip" true 2>/dev/null; do
  sleep 5
  kill -0 "$vmpid" 2>/dev/null || { echo "vm exited" >&2; exit 1; }
done
echo "ssh up after $(( $(date +%s) - t0 ))s" >&2
ssh $sshopts "x18@$ip" 'sw_vers; uname -a; id' >&2
scp $sshopts "$guestd" "$here/org.wraithbox.x18-guestd.plist" "x18@$ip:/tmp/"
ssh $sshopts "x18@$ip" "echo '$pass' | sudo -S sh -c '
  mkdir -p /usr/local/libexec &&
  install -m 755 -o root -g wheel /tmp/x18-guestd /usr/local/libexec/x18-guestd &&
  install -m 644 -o root -g wheel /tmp/org.wraithbox.x18-guestd.plist /Library/LaunchDaemons/ &&
  launchctl bootstrap system /Library/LaunchDaemons/org.wraithbox.x18-guestd.plist'" >&2
sleep 3
ssh $sshopts "x18@$ip" "echo '$pass' | sudo -S sh -c 'launchctl print system/org.wraithbox.x18-guestd | grep -E \"state|pid\"; cat /var/log/x18-guestd.log'" >&2
ssh $sshopts "x18@$ip" "echo '$pass' | sudo -S shutdown -h now" >&2 || true
wait "$vmpid"
echo "provisioned in $(( $(date +%s) - t0 ))s" >&2
