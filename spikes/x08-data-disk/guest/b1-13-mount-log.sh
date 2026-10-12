# X08 boot 1: the kernel log of one failed mount of the data volume. Throwaway.
set -u
mount_apfs -o nobrowse /dev/disk5s1 /Volumes/wbhomes 2>&1; echo "exit $?"
sleep 1
log show --last 20s --style compact --predicate 'sender == "apfs" OR process == "apfsd" OR process == "apfs_keybag" OR eventMessage CONTAINS "disk5"' 2>/dev/null | grep -v 'Df log\[' | tail -40
diskutil apfs listCryptoUsers disk5s1 2>&1
diskutil apfs list disk5 | grep -E 'FileVault|Encrypt|Locked'
true
