#!/bin/sh
# gssh.sh <bundle> <mac> [command...]: run a command in a provisioned test
# guest over SSH as wbdev, with the password from the bundle. Throwaway
# spike helper for I228.
# gssh.sh <bundle> <mac> --put <local> <remote>: copy a file in.
set -eu
bundle="$1"
mac="$2"
shift 2
ip=$(awk -v hw="hw_address=1,$mac" '
  /^\{/ { addr = "" }
  /ip_address=/ { sub(/.*ip_address=/, ""); addr = $0 }
  index($0, hw) { print addr; exit }' /var/db/dhcpd_leases)
[ -n "$ip" ] || { echo "no lease for $mac" >&2; exit 2; }
pw="$HOME/Library/Caches/wraithbox-spikes/$bundle/ssh-password"
opts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5 -o PubkeyAuthentication=no -o PreferredAuthentications=password"
# sshpass now and then has its password refused (exit 5, or 255 from ssh) on this guest,
# before any command runs, so the call is tried up to three times.
try() {
  n=0
  while :; do
    rc=0
    "$@" || rc=$?
    n=$((n + 1))
    { [ "$rc" = 5 ] || [ "$rc" = 255 ]; } && [ "$n" -lt 3 ] || return "$rc"
    sleep 2
  done
}
if [ "${1:-}" = "--put" ]; then
  # shellcheck disable=SC2086 # opts is a list of options
  try sshpass -f "$pw" scp $opts "$2" "wbdev@$ip:$3"
  exit
fi
# shellcheck disable=SC2086 # opts is a list of options
try sshpass -f "$pw" ssh $opts "wbdev@$ip" "$@"
