# X08 cycle 2: what wb-guestd would do at start. Find the data volume by
# its name ($VOLNAME), move it from wherever Disk Arbitration put it to the
# fixed mount point $H outside /Volumes (cycle 1: a /Volumes point can't be
# mounted on again), with nobrowse, nosuid and nodev unless SUID=1, and
# with ownership on: the owners mount option, then diskutil enableOwnership,
# which an external volume needs again on a new system disk. Throwaway.
set -u
t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
vol=$(diskutil info "$VOLNAME" 2>/dev/null | awk -F: '/Device Identifier/{gsub(/ /,"",$2);print $2}')
echo "volume: ${vol:-none}"
[ -n "$vol" ] || exit 1
echo "before: $(mount | grep "^/dev/$vol " || echo 'not mounted')"
diskutil info "$vol" | grep -E 'Owners|Locked|FileVault'
diskutil unmount "$vol" >/dev/null 2>&1
# An unmount locks a passphrase volume again, so unlock after it.
if [ -n "${PASS:-}" ] && diskutil info "$vol" | grep -q 'Locked: *Yes'; then
  printf '%s' "$PASS" | diskutil apfs unlockVolume "$vol" -stdinpassphrase -nomount
  diskutil info "$vol" | grep -E 'Locked'
fi
mkdir -p "$H"
opts=nobrowse,nosuid,nodev,owners
[ "${SUID:-0}" = 1 ] && opts=nobrowse,owners
diskutil mount -mountOptions "$opts" -mountPoint "$H" "$vol"
echo "mounted: $(mount | grep " $H ")"
diskutil enableOwnership "$H"
t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
echo "TIME mount $(echo "$t1 - $t0" | bc) s"
echo "after: $(mount | grep "^/dev/$vol ")"
diskutil info "$vol" | grep -E 'Owners|Mount Point'
ls -lan "$H"
