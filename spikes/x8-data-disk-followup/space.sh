#!/bin/sh
# space.sh <bundle> <mac> <uuid> <MiB> [dir]: the space a host-made data volume
# gives back to the host. Prints the data disk's allocated bytes on the
# host (st_blocks * 512) before a write in the guest, after a write of
# <MiB> MiB of random data and a sync, and every 10 s for 3 minutes after
# the file is deleted and synced. Throwaway spike helper for I228.
set -eu
bundle="$1"; mac="$2"; uuid="$3"; mib="$4"; dir="${5:-/private/var/wbhomes}"
here=$(dirname "$0")
disk="$HOME/Library/Caches/wraithbox-spikes/$bundle/data.wbdisk"
pw="$HOME/Library/Caches/wraithbox-spikes/$bundle/ssh-password"
alloc() { echo $(( $(stat -f %b "$disk") * 512 )); }
g() { "$here/gssh.sh" "$bundle" "$mac" "$@" < "$pw"; }
used() { g "diskutil info $uuid | awk -F': *' '/Volume Used Space/ { print \$2 }'"; }
echo "before: host $(alloc) B; guest used $(used)"
t0=$(date +%s)
[ "$mib" = 0 ] || g "sudo -S -p '' sh -c 'dd if=/dev/urandom of=$dir/fill bs=1m count=$mib 2>/dev/null; sync'"
echo "after write+sync of $mib MiB ($(( $(date +%s) - t0 )) s): host $(alloc) B; guest used $(used)"
g "sudo -S -p '' sh -c 'rm $dir/fill; sync'"
t0=$(date +%s)
for _ in $(seq 0 "${POLLS:-18}"); do
  echo "deleted +$(( $(date +%s) - t0 )) s: host $(alloc) B"
  sleep 10
done
echo "end: guest used $(used)"
