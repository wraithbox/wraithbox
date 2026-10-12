# X08 boot 4: unlock the passphrase volume by hand on the new system disk
# and read the kernel's reason if it fails. Throwaway.
set -u
diskutil info WBPass | grep -E 'FileVault|Locked|Encrypt'
diskutil apfs list | grep -A8 'WBPass' | grep -E 'FileVault|Encrypt|Locked'
diskutil apfs listCryptoUsers WBPass 2>&1
printf '%s' x08-pass | diskutil apfs unlockVolume WBPass -stdinpassphrase 2>&1; echo "exit $?"
log show --style compact --last 15s --predicate 'process == "kernel"' 2>/dev/null | grep -iE 'apfs|keystore|unwrap|unlock' | grep -v spaceman | tail -12
mount | grep -i wbpass
true
