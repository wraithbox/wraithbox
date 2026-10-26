#!/bin/sh
# boot-check.sh <bundle> <mac> <uuid> <first: true|false>: after a test
# guest's boot with the host-made data disk, wait for SSH, copy the
# tools in, print diskutil's report of the volume, run wb-guestd's data
# mount (x8mount) as root, the report again, and the datamount
# conformance check TestMounted as the SSH user. Run from the
# repository root, after `mise run conformance:build` and building
# .scratch/x8mount. Throwaway spike helper for I228.
set -u
bundle="$1"; mac="$2"; uuid="$3"; first="$4"
here=$(dirname "$0")
g() { "$here/gssh.sh" "$bundle" "$mac" "$@"; }
pw="$HOME/Library/Caches/wraithbox-spikes/$bundle/ssh-password"
i=0
until g true 2>/dev/null; do
  i=$((i + 1)); [ "$i" -lt 90 ] || { echo "no SSH"; exit 1; }
  sleep 5
done
g --put "$here/guest-report.sh" /tmp/guest-report.sh
g --put .scratch/x8mount /tmp/x8mount
g --put packages/wraithbox-go/.build/conformance/datamount.test /tmp/datamount.test
g "chmod +x /tmp/guest-report.sh /tmp/x8mount /tmp/datamount.test"
echo "##### report at boot"
g "/tmp/guest-report.sh $uuid"
echo "##### wb-guestd data mount (first mount: $first)"
g "sudo -S -p '' /tmp/x8mount -uuid $uuid -first=$first" < "$pw"
echo "##### report after the mount"
g "/tmp/guest-report.sh $uuid"
echo "##### TestMounted as the SSH user"
g "cd / && /tmp/datamount.test -test.run TestMounted -test.v"
