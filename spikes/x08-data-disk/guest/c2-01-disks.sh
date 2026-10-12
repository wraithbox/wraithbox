# X08 cycle 2, boot 3 on g1: the cycle 1 data volume (internal virtio disk)
# again on the system disk that made it, and a blank USB mass storage disk,
# which the guest sees as external. Format the USB disk APFS and look at
# it. Then add a passphrase-protected volume to the cycle 1 container, as a
# second candidate. Throwaway.
set -u
diskutil list
echo "-- cycle 1 volume on its own system disk:"
diskutil info WBData | grep -E 'Device Identifier|Mounted|Mount Point|Owners'
mount | grep -i wbdata || { diskutil mount WBData 2>&1; mount | grep -i wbdata; }
usb=""
for d in $(diskutil list | awk -F"[/ ]" '/^\/dev\/disk/{print $3}'); do
  info=$(diskutil info $d 2>/dev/null) || continue
  echo "$info" | grep -q 'Device Location: *External' || continue
  echo "$info" | grep -q 'Content (IOContent): *None' && usb=$d
done
echo "usb disk: ${usb:-none}"
[ -n "$usb" ] || exit 1
diskutil info $usb | grep -E 'Media Name|Protocol|Device Location|Removable|Ejectable|Disk Size|Solid'
t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
diskutil eraseDisk APFS WBHomes GPT $usb
t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
echo "TIME format $(echo "$t1 - $t0" | bc) s"
diskutil info WBHomes | grep -E 'Device Identifier|Mount Point|Owners|Volume UUID|Device Location|Removable|FileVault|Encrypt'
diskutil apfs list | grep -B2 -A10 'WBHomes' | grep -E 'Name|FileVault|Encrypt|Container'
mount | grep -i wbhomes
echo "-- passphrase volume in the cycle 1 container:"
c=$(diskutil info WBData | awk -F: '/APFS Container:/{gsub(/ /,"",$2);print $2}')
diskutil apfs addVolume "$c" APFS WBPass -passphrase x08-pass 2>&1 | tail -3
diskutil info WBPass | grep -E 'Device Identifier|Mount Point|FileVault|Encrypt|Locked'
diskutil apfs listCryptoUsers WBPass 2>&1
echo "on the passphrase volume" > /Volumes/WBPass/marker && cat /Volumes/WBPass/marker
sync
true
