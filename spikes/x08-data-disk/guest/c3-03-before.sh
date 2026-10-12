# X08 cycle 3, a new system disk: the data volumes before wb-guestd has done
# anything, and before the users exist. Throwaway.
set -u
echo "boot time: $(sysctl -n kern.boottime)"
for v in WBHost WBHfs; do
  echo "-- $v"; diskutil info $v | grep -E 'Device Identifier|Mounted|Mount Point|Owners|FileVault|Encrypt|Locked'
done
mount | grep -iE 'wbhost|wbhfs'
echo "-- when Disk Arbitration mounted them, from the log:"
log show --style compact --last 3m --predicate 'process == "diskarbitrationd"' 2>/dev/null | grep -E 'mounted disk' | head -6
echo "-- homes, numeric, with no such users here:"
ls -lan /Volumes/WBHost /Volumes/WBHfs 2>&1
ls -lane /Volumes/WBHost/wbr1 2>&1 | head -5
dscl . -list /Users UniqueID | awk '$2>=500'
echo "-- an existing non-project user (wbp-test, 502):"
sudo -u wbp-test /bin/sh -c 'ls /Volumes/WBHost/wbr1 2>&1 | head -1; ls /Volumes/WBHfs/wbs1 2>&1 | head -1'
true
