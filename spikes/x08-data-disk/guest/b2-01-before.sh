# X08 boot 2 (fresh system disk, same data disk): what the data volume looks
# like before wb-guestd has done anything. Throwaway.
set -u
echo "boot time: $(sysctl -n kern.boottime)  now: $(date)"
diskutil list | sed -n '1,12p'
mount | grep -iE 'wbdata|wbhomes' || echo "data volume not mounted"
diskutil info WBData | grep -E 'Device Identifier|Mount Point|Owners|Volume UUID|Mounted'
echo "-- Disk Arbitration's mount of it, from the log since boot:"
log show --style compact --start "$(date -r $(sysctl -n kern.boottime | awk -F'[ ,]' '{print $4}') '+%Y-%m-%d %H:%M:%S')" \
  --predicate 'process == "diskarbitrationd" OR (process == "kernel" AND eventMessage CONTAINS "WBData")' 2>/dev/null \
  | grep -iE 'WBData|mounted disk|disk.s1' | head -12
echo "-- the volume root and homes, numeric:"
ls -lane /Volumes/WBData 2>&1
ls -lan /Volumes/WBData/wbp1 2>&1 | head -6
echo "-- users on this system disk:"
dscl . -list /Users UniqueID | awk '$2>=500'
dscl . -read /Users/wbp1 2>&1 | head -2
echo "-- an existing non-project user (wbp-test, 502) against the homes:"
sudo -u wbp-test /bin/sh -c 'ls /Volumes/WBData/wbp1 2>&1 | head -1; cat /Volumes/WBData/wbp1/.claude.json 2>&1 | head -1'
true
