#!/bin/sh
# X05-fs-benchmark spike, in the guest: after a kill, check the data
# volume with fsck_apfs (read-only), mount it again, and run crash.mjs check.
set -u
dev=$(mount | awk '$3 == "/Volumes/data" { print $1 }')
echo "data volume: $dev"
sudo -n diskutil unmount /Volumes/data
sudo -n fsck_apfs -n "$dev"
echo "fsck_apfs exit: $?"
sudo -n diskutil mount "$dev"
/Users/x05/tools/node/bin/node /Users/x05/crash.mjs check /Volumes/data/crash
