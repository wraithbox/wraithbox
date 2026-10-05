#!/bin/sh
# X05-fs-benchmark spike, in the guest: format the blank data disk as APFS
# volume "data" (mounted at /Volumes/data), turn Spotlight off on it, and
# give it to the spike user. The guest formats it: the host never mounts
# or formats a data disk.
# Usage: datadisk.sh <size in bytes>
set -eu
want=$1
for d in disk0 disk1 disk2 disk3 disk4 disk5 disk6; do
  info=$(diskutil info "$d" 2>/dev/null) || continue
  echo "$info" | grep -q "Whole: *Yes" || continue
  echo "$info" | grep -q "($want Bytes)" || continue
  echo "data disk is $d"
  sudo -n diskutil eraseDisk APFS data GPT "$d"
  break
done
mount | grep " /Volumes/data "
sudo -n mdutil -i off /Volumes/data || true
sudo -n chown x05:staff /Volumes/data
df -h /Volumes/data
