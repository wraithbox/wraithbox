#!/bin/sh
# X02-warm-start: copy the vsock probe into a stopped guest's Data volume
# as a LaunchDaemon, without booting the guest and without sudo.
# Usage: inject.sh <sys.img> <x02-echo binary> <plist>
# The volume is attached with ownership ignored, so new files are stored
# as uid 99 (_unknown), which the guest shows to root as owned by root.
set -eu
img=$1
bin=$2
plist=$3
dev=$(hdiutil attach -nomount -imagekey diskimage-class=CRawDiskImage "$img" | awk 'NR==1{print $1}')
echo "attached $dev"
trap 'hdiutil detach "$dev" >/dev/null 2>&1 || true' EXIT
diskutil list "$dev"
data=""
for v in $(diskutil list "$dev" | awk '/APFS Volume/ {print $NF}'); do
  role=$(diskutil info "$v" | awk -F: '/APFS Volume Role|Volume Role/ {gsub(/^ +/,"",$2); print $2}' | head -1)
  name=$(diskutil info "$v" | awk -F: '/Volume Name/ {gsub(/^ +/,"",$2); print $2}')
  echo "$v role=$role name=$name"
  case "$role" in *Data*) data=$v ;; esac
done
[ -n "$data" ] || { echo "no Data volume"; exit 1; }
diskutil info "$data" | grep -Ei 'encrypt|filevault|locked|owners' || true
mnt=$HOME/Library/Caches/wraithbox-spikes/x02/mnt; mkdir -p "$mnt"
diskutil mount -mountPoint "$mnt" "$data"
mkdir -p "$mnt/private/var/db" "$mnt/usr/local/libexec" "$mnt/Library/LaunchDaemons"
ls -la "$mnt" | head -30
cp -X "$bin" "$mnt/usr/local/libexec/x02-echo"
chmod 755 "$mnt/usr/local/libexec/x02-echo"
cp -X "$plist" "$mnt/Library/LaunchDaemons/org.wraithbox.x02-echo.plist"
chmod 644 "$mnt/Library/LaunchDaemons/org.wraithbox.x02-echo.plist"
xattr -c "$mnt/usr/local/libexec/x02-echo" "$mnt/Library/LaunchDaemons/org.wraithbox.x02-echo.plist" || true
ls -ln "$mnt/usr/local/libexec" "$mnt/Library/LaunchDaemons"
diskutil unmount "$mnt"
rmdir "$mnt"
