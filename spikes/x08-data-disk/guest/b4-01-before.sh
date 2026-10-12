# X08 boot 4 (g2, a second boot of that system disk, which has never had
# the data volumes unlocked): both data volumes before wb-guestd does
# anything. Throwaway.
set -u
echo "boot time: $(sysctl -n kern.boottime)"
mount | grep -iE 'wbdata|wbpass|wbhomes' || echo "no data volume mounted"
for v in WBData WBPass; do
  echo "-- $v"; diskutil info $v | grep -E 'Device Identifier|Mounted|Owners|FileVault|Locked|Volume UUID'
done
dscl . -list /Users UniqueID | awk '$2>=500'
true
