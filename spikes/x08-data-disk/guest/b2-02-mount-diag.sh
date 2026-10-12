# X08 boot 2: Disk Arbitration failed to mount the data volume at boot. Try
# by hand and read the kernel's reason. Throwaway.
set -u
v=$(diskutil info WBData | awk -F: '/Device Identifier/{gsub(/ /,"",$2);print $2}')
echo "volume $v"
echo "-- kernel apfs log at boot for $v:"
log show --style compact --last 10m --predicate 'process == "kernel" AND sender == "apfs"' 2>/dev/null | grep -E "$v|disk3 " | head -30
echo "-- diskutil mount:"; diskutil mount $v 2>&1
echo "-- mount_apfs to a fresh dir:"; mkdir -p /private/var/x08try; mount_apfs -o nobrowse,nosuid,nodev /dev/$v /private/var/x08try 2>&1; echo "exit $?"
echo "-- kernel apfs log of that try:"
log show --style compact --last 15s --predicate 'process == "kernel"' 2>/dev/null | grep -iE "apfs|$v|keybag|unlock|crypt" | head -20
diskutil apfs list | grep -A12 "WBData" | head -14
diskutil apfs listCryptoUsers $v 2>&1
ls -la /System/Volumes/xarts 2>&1 | head
true
