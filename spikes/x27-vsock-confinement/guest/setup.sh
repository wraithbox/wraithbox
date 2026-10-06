#!/bin/sh
# X27-vsock-confinement: boot a fresh clone of an installed (never booted)
# bundle with guest provisioning, then over SSH: install x27-guestd as a
# root LaunchDaemon (KeepAlive) on vsock port 1024, create a standard
# (non-admin) user "proj1" standing in for a project user, and copy the
# probe and the Seatbelt profiles in. Leaves the VM running; the
# experiments run from run.sh. Throwaway; the product path is X17-image-build.
#
# Usage: setup.sh <x27 binary> <pristine bundle> <new bundle> <scratch dir>
set -eu
x27=$1 pristine=$2 bundle=$3 scratch=$4
here=$(cd "$(dirname "$0")" && pwd)
rm -rf "$bundle"
cp -c -R "$pristine" "$bundle"
# A fresh MAC so the DHCP lease lookup can't pick up an older lease.
mac=$(printf '2a:%02x:%02x:%02x:%02x:%02x' $(od -An -N5 -tu1 /dev/urandom))
sed -E -i '' "s/\"mac\":\"[^\"]*\"/\"mac\":\"$mac\"/" "$bundle/cfg.json"
leasemac=$(echo "$mac" | awk -F: '{for(i=1;i<=NF;i++){sub(/^0/,"",$i); printf "%s%s",$i,(i<NF?":":"")}}')
pass=$(openssl rand -hex 12)
printf '%s\n' "$pass" >"$scratch/guest-pass"
printf '#!/bin/sh\necho %s\n' "$pass" >"$scratch/askpass"
chmod 700 "$scratch/askpass"
echo "mac $mac" >&2

"$x27" run "$bundle" "$scratch/ctl.sock" 180 x27 "$pass" >"$scratch/vm.jsonl" 2>"$scratch/vm.err" &
echo $! >"$scratch/vm.pid"
t0=$(date +%s)
ip=""
while [ -z "$ip" ]; do
  sleep 5
  kill -0 "$(cat "$scratch/vm.pid")" 2>/dev/null || { echo "vm exited" >&2; exit 1; }
  ip=$(awk -v m="1,$leasemac" '/ip_address=/{split($0,a,"=");ipa=a[2]} /hw_address=/{split($0,b,"=");if(b[2]==m){print ipa}}' /var/db/dhcpd_leases 2>/dev/null | tail -1)
done
echo "$ip" >"$scratch/guest-ip"
echo "guest ip $ip after $(( $(date +%s) - t0 ))s" >&2

export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
sshopts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no -o LogLevel=ERROR"
until ssh $sshopts "x27@$ip" true 2>/dev/null; do
  sleep 5
done
echo "ssh up after $(( $(date +%s) - t0 ))s" >&2
ssh $sshopts "x27@$ip" 'sw_vers; uname -a; id; csrutil status' >&2
ssh $sshopts "x27@$ip" 'mkdir -p /tmp/x27'
scp $sshopts "$scratch/x27-guestd" "$scratch/x27-probe" "$here/org.wraithbox.x27-guestd.plist" "$here"/profiles/*.sb "x27@$ip:/tmp/x27/"
ssh $sshopts "x27@$ip" "echo '$pass' | sudo -S sh -c '
  mkdir -p /usr/local/libexec /usr/local/bin /usr/local/share/x27 &&
  install -m 755 -o root -g wheel /tmp/x27/x27-guestd /usr/local/libexec/x27-guestd &&
  install -m 755 -o root -g wheel /tmp/x27/x27-probe /usr/local/bin/x27-probe &&
  install -m 644 -o root -g wheel /tmp/x27/*.sb /usr/local/share/x27/ &&
  install -m 644 -o root -g wheel /tmp/x27/org.wraithbox.x27-guestd.plist /Library/LaunchDaemons/ &&
  sysadminctl -addUser proj1 -fullName \"project user\" -password \"$pass\" &&
  launchctl bootstrap system /Library/LaunchDaemons/org.wraithbox.x27-guestd.plist'" >&2
sleep 2
ssh $sshopts "x27@$ip" "echo '$pass' | sudo -S sh -c 'id proj1; dseditgroup -o checkmember -m proj1 admin; launchctl print system/org.wraithbox.x27-guestd | grep -E \"state|pid|runs\"; cat /var/log/x27-guestd.log'" >&2
echo "ready after $(( $(date +%s) - t0 ))s" >&2
