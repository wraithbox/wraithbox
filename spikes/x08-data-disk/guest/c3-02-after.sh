# X08 boot 5: after the guest wrote to them, are the volumes still without
# encryption at rest? Throwaway.
set -u
for v in WBHost WBHfs; do
  echo "-- $v"; diskutil info $v | grep -E 'FileVault|Encrypt|Owners|Mount Point'
done
diskutil apfs list | grep -A10 'Name: *WBHost' | grep -E 'FileVault|Encrypt'
true
