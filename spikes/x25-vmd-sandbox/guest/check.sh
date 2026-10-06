#!/bin/sh
# While a VM with a NAT NIC (MAC <mac>) runs, log in over SSH and check from
# inside the guest what the extra devices give it: the internet through
# NAT, the virtio-fs share, and the extra disk's marker.
# Usage: check.sh <scratch dir with guest-pass and askpass> <nat mac> <marker>
set -u
scratch=$1 mac=$2 marker=$3
pass=$(cat "$scratch/guest-pass")
leasemac=$(echo "$mac" | awk -F: '{for(i=1;i<=NF;i++){sub(/^0/,"",$i); printf "%s%s",$i,(i<NF?":":"")}}')
export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
sshopts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no"
t0=$(date +%s)
ip=""
while [ $(( $(date +%s) - t0 )) -lt 120 ]; do
  ip=$(awk -v m="1,$leasemac" '/ip_address=/{split($0,a,"=");ipa=a[2]} /hw_address=/{split($0,b,"=");if(b[2]==m){print ipa}}' /var/db/dhcpd_leases 2>/dev/null | tail -1)
  if [ -n "$ip" ] && ssh $sshopts "x18@$ip" true 2>/dev/null; then break; fi
  ip=""
  sleep 3
done
if [ -z "$ip" ]; then echo "guest: no SSH over NAT within 120 s"; exit 1; fi
echo "guest: SSH over NAT at $ip after $(( $(date +%s) - t0 )) s"
# shellcheck disable=SC2087 # expand pass and marker on the host on purpose
ssh $sshopts "x18@$ip" sh -s <<EOF
echo "--- internet through NAT"
curl -sS -o /dev/null -w 'https://www.apple.com/ -> HTTP %{http_code}\n' --max-time 10 https://www.apple.com/ 2>&1
echo "--- virtio-fs share"
mkdir -p /tmp/x25share
echo '$pass' | sudo -S mount_virtiofs x25share /tmp/x25share 2>&1 && cat /tmp/x25share/secret.txt 2>&1
echo "--- extra disk"
for d in \$(diskutil list | sed -nE 's#^(/dev/disk[0-9]+) .*physical.*#\1#p'); do
  if echo '$pass' | sudo -S head -c 64 "\$d" 2>/dev/null | grep -q '$marker'; then echo "marker found on \$d"; fi
done
echo "--- done"
EOF
