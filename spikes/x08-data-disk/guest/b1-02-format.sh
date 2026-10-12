# X08 boot 1: format the blank data disk APFS, then look at it. Throwaway.
# The data disk is found by what it is, not by its number: the VirtIO disk
# with no partition scheme (IOContent None) that is not the boot disk.
set -u
dev=""
for d in $(diskutil list -plist physical | plutil -extract WholeDisks raw -o - - 2>/dev/null; true); do :; done
for d in disk0 disk1 disk2 disk3; do
  c=$(diskutil info $d 2>/dev/null | awk -F: '/Content \(IOContent\)/{gsub(/^ +/,"",$2);print $2}')
  m=$(diskutil info $d 2>/dev/null | awk -F: '/Media Name/{gsub(/^ +/,"",$2);print $2}')
  if [ "$c" = "None" ] && [ "$m" = "Apple Inc. VirtIO Block Media" ]; then dev=$d; fi
done
echo "data disk: $dev"
[ -n "$dev" ] || exit 1
t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
diskutil eraseDisk APFS WBData GPT $dev
t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
echo "TIME format $(echo "$t1 - $t0" | bc) s"
diskutil list $dev
diskutil apfs list
vol=$(diskutil info WBData | awk -F: '/Device Identifier/{gsub(/ /,"",$2);print $2}')
echo "volume: $vol"
diskutil info $vol
mount | grep WBData
ls -la@e /Volumes/WBData
true
