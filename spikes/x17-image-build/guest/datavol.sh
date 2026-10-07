#!/bin/sh
# X17-image-build: attach a stopped bundle's system disk on the host, as the
# user (no sudo), and mount its Data volume.
# Usage: datavol.sh attach <sys.img> <mountpoint> [ro]   prints the whole-disk device
#        datavol.sh detach <mountpoint> <disk>
# Throwaway.
set -eu
case "$1" in
attach)
  img=$2 mnt=$3 ro=${4:-}
  flags=""
  [ "$ro" = ro ] && flags="-readonly"
  out=$(hdiutil attach -nomount $flags -imagekey diskimage-class=CRawDiskImage "$img")
  echo "$out" >&2
  whole=$(echo "$out" | awk 'NR==1{print $1}')
  # The APFS physical store is a partition of $whole; find its container and the Data volume.
  vol=$(diskutil apfs list -plist | python3 -c '
import plistlib, sys
whole = sys.argv[1].replace("/dev/", "")
d = plistlib.loads(sys.stdin.buffer.read())
for c in d["Containers"]:
    if any(s["DeviceIdentifier"].startswith(whole + "s") for s in c["PhysicalStores"]):
        for v in c["Volumes"]:
            if "Data" in v.get("Roles", []):
                print(v["DeviceIdentifier"], v.get("Name"), v.get("FileVault"), v.get("Encryption"), file=sys.stderr)
                print(v["DeviceIdentifier"])
' "$whole")
  [ -n "$vol" ] || { echo "no Data volume on $whole" >&2; exit 1; }
  mkdir -p "$mnt"
  mopts="nobrowse"
  [ "$ro" = ro ] && mopts="nobrowse,rdonly"
  diskutil mount -mountOptions "$mopts" -mountPoint "$mnt" "$vol" >&2
  mount | grep " $mnt " >&2 || true
  echo "$whole"
  ;;
detach)
  mnt=$2 whole=$3
  diskutil unmount "$mnt" >&2 || true
  hdiutil detach "$whole" >&2
  ;;
*) echo "usage" >&2; exit 2 ;;
esac
