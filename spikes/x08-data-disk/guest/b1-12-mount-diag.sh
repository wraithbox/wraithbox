# X08 boot 1: the remount failed. Why. Throwaway.
set -u
diskutil info disk5s1 | grep -E 'Mounted|Mount Point|Owners|Locked'
mount | grep disk5 || echo "disk5 not mounted"
ls -lad /Volumes/wbhomes
echo "-- mount with -mountOptions, verbose error:"
diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint /Volumes/wbhomes disk5s1 2>&1
echo "-- mount_apfs directly:"
mount_apfs -o nobrowse,nosuid,nodev /dev/disk5s1 /Volumes/wbhomes 2>&1; echo "exit $?"
mount | grep disk5
log show --last 3m --style compact --predicate 'process == "diskarbitrationd" OR process == "mount_apfs" OR sender == "apfs"' 2>/dev/null | grep -iE 'disk5|mount|wbhomes' | tail -15
true
