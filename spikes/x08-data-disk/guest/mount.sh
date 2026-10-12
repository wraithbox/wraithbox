# X08: what wb-guestd would do at start. Find the data volume by its name,
# move it from wherever Disk Arbitration put it to the fixed mount point,
# with nobrowse, and with nosuid and nodev unless SUID=1. Throwaway.
set -u
mp=/Volumes/wbhomes
t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
vol=$(diskutil info WBData 2>/dev/null | awk -F: '/Device Identifier/{gsub(/ /,"",$2);print $2}')
echo "volume: ${vol:-none}"
[ -n "$vol" ] || exit 1
echo "before:"; mount | grep "^/dev/$vol " || echo "  not mounted"
diskutil unmount "$vol" >/dev/null 2>&1
mkdir -p $mp
opts=nobrowse,nosuid,nodev
[ "${SUID:-0}" = 1 ] && opts=nobrowse
diskutil mount -mountOptions "$opts" -mountPoint $mp "$vol"
diskutil enableOwnership $mp
t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
echo "TIME mount $(echo "$t1 - $t0" | bc) s"
echo "after:"; mount | grep "^/dev/$vol "
diskutil info "$vol" | grep -E 'Owners|Mount Point'
ls -la $mp
