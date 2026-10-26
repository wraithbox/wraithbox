#!/bin/sh
# remount.sh <device> default|<point> [options]: run as root in the test
# guest. Unmounts the data volume and mounts it again, where Disk
# Arbitration would (default) or at <point> with diskutil's
# -mountOptions. Throwaway spike helper for I228.
set -eu
dev="$1"; where="$2"; opts="${3:-}"
diskutil unmount "$dev"
if [ "$where" = default ]; then
  diskutil mount "$dev"
elif [ -n "$opts" ]; then
  mkdir -p "$where"
  diskutil mount -mountOptions "$opts" -mountPoint "$where" "$dev"
else
  mkdir -p "$where"
  diskutil mount -mountPoint "$where" "$dev"
fi
mount | grep "^/dev/$dev "
