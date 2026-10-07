#!/bin/sh
# X17-image-build: the path that works for an unprivileged host. First boot
# with VZMacGuestProvisioningOptions (a provisioning admin user, Remote Login
# on) on a NAT network that only this boot has. One SSH session installs
# x17-guestd as a root LaunchDaemon. From then on the host talks to x17-guestd
# over vsock only: the boot tool connects as soon as it listens and sends
# <requests> (normally: scan, cleanup, scan, shutdown).
# Usage: provision-ssh.sh <x17> <bundle> <x17-guestd> <scratch> <requests> <out.jsonl> <password file>
# The password file must exist; the requests may use the password to undo
# the provisioning (cleanup.sh), so this script leaves the file in place.
# Throwaway; adapted from X18-vsock-handoff's setup.sh.
set -eu
x17=$1 bundle=$2 guestd=$3 scratch=$4 req=$5 out=$6 pwfile=$7
here=$(cd "$(dirname "$0")" && pwd)
user=wbprov
printf '#!/bin/sh\ncat "%s"\n' "$pwfile" >"$scratch/askpass"
chmod 700 "$scratch/askpass"
mac=$(sed -E 's/.*"mac":"([^"]*)".*/\1/' "$bundle/cfg.json")
leasemac=$(echo "$mac" | awk -F: '{for(i=1;i<=NF;i++){sub(/^0/,"",$i); printf "%s%s",$i,(i<NF?":":"")}}')

t0=$(date +%s)
"$x17" boot "$bundle" "$req" --nat --timeout 900 --provision "$user" "$pwfile" 1 0 >"$out" 2>"$out.err" &
vmpid=$!

ip=""
while [ -z "$ip" ]; do
  sleep 2
  kill -0 "$vmpid" 2>/dev/null || { echo "vm exited" >&2; exit 1; }
  ip=$(awk -v m="1,$leasemac" '/ip_address=/{split($0,a,"=");ipa=a[2]} /hw_address=/{split($0,b,"=");if(b[2]==m){print ipa}}' /var/db/dhcpd_leases 2>/dev/null | tail -1)
done
echo "guest ip $ip after $(( $(date +%s) - t0 ))s" >&2

export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
sshopts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no"
until ssh $sshopts "$user@$ip" true 2>/dev/null; do
  sleep 2
  kill -0 "$vmpid" 2>/dev/null || { echo "vm exited" >&2; exit 1; }
done
echo "ssh up after $(( $(date +%s) - t0 ))s" >&2
scp $sshopts "$guestd" "$here/org.wraithbox.x17-guestd.plist" "$user@$ip:/tmp/"
ssh $sshopts "$user@$ip" "sudo -S -p '' sh -c '
  mkdir -p /usr/local/libexec &&
  install -m 755 -o root -g wheel /tmp/x17-guestd /usr/local/libexec/x17-guestd &&
  install -m 644 -o root -g wheel /tmp/org.wraithbox.x17-guestd.plist /Library/LaunchDaemons/ &&
  rm -f /tmp/x17-guestd /tmp/org.wraithbox.x17-guestd.plist &&
  launchctl bootstrap system /Library/LaunchDaemons/org.wraithbox.x17-guestd.plist'" <"$pwfile" >&2
echo "x17-guestd installed after $(( $(date +%s) - t0 ))s" >&2
rm -f "$scratch/askpass"
wait "$vmpid"
echo "provisioning boot done after $(( $(date +%s) - t0 ))s" >&2
