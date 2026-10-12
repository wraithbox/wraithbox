# X08 cycle 3, boot 5 on g2: the APFS volume the host made (WBHost) as the
# guest sees it, and a blank internal disk the guest formats HFS+ (WBHfs),
# which has no encryption at rest. Throwaway.
set -u
diskutil list | grep -E '^/dev|WB'
echo "-- WBHost as the guest sees it:"
diskutil info WBHost | grep -E 'Device Identifier|Mounted|Mount Point|Owners|FileVault|Encrypt|Device Location|Locked'
diskutil apfs list | grep -A10 'Name: *WBHost' | grep -E 'FileVault|Encrypt'
mount | grep -i wbhost
blank=""
for d in $(diskutil list | awk -F"[/ ]" '/^\/dev\/disk/{print $3}'); do
  info=$(diskutil info $d 2>/dev/null) || continue
  echo "$info" | grep -q 'Media Name: *Apple Inc. VirtIO Block Media' || continue
  echo "$info" | grep -q 'Content (IOContent): *None' && blank=$d
done
echo "blank disk: ${blank:-none}"
[ -n "$blank" ] || exit 1
t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
diskutil eraseDisk JHFS+ WBHfs GPT $blank
t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
echo "TIME format HFS+ $(echo "$t1 - $t0" | bc) s"
diskutil info WBHfs | grep -E 'Device Identifier|Mount Point|Owners|FileVault|Encrypt|File System Personality|Device Location'
mount | grep -i wbhfs
true
