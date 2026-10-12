# X08 boot 6: the remount of WBHost failed after an unmount. Why. Throwaway.
set -u
mount_apfs -o nobrowse,nosuid,nodev /dev/disk4s1 /private/var/wbhomes 2>&1; echo "mount_apfs exit $?"
log show --style compact --last 20s --predicate 'process == "kernel" OR process == "diskarbitrationd"' 2>/dev/null | grep -iE 'apfs_vfsop_mount|apfs_mount|disk4s1|mount' | grep -v spaceman | tail -10
ls -lde@O /private/var/wbhomes
echo "-- fresh mount point:"
mkdir -p /private/var/wbhomes-b
diskutil mount -mountOptions nobrowse,nosuid,nodev,owners -mountPoint /private/var/wbhomes-b disk4s1 2>&1
mount | grep disk4s1
diskutil unmount disk4s1 2>&1
echo "-- the old point again:"
diskutil mount -mountOptions nobrowse,nosuid,nodev,owners -mountPoint /private/var/wbhomes disk4s1 2>&1
mount | grep disk4s1
true
