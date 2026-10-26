#!/bin/sh
# bisect-mount.sh <bundle> <mac> <uuid> <device>: which mount of the
# data volume gives the space of a deleted file back to the host. For
# each mount point and option set, remounts the volume, writes 512 MiB,
# deletes it, and watches the image for 30 s. Throwaway spike helper for
# I228.
set -u
bundle="$1"; mac="$2"; uuid="$3"; dev="$4"
here=$(dirname "$0")
run() {
  echo "=== mount at $1, options ${2:-none}"
  "$here/groot.sh" "$bundle" "$mac" "$here/remount.sh" "$dev" "$@" 2>&1 | tail -1
  POLLS=3 "$here/space.sh" "$bundle" "$mac" "$uuid" 512 "$1" 2>&1 | grep -v "Permission denied"
}
for c in ${CONFIGS:-a b c d e f}; do
  case $c in
    a) run /private/var/x8a ;;
    b) run /private/var/x8b nobrowse ;;
    c) run /private/var/x8c nosuid,nodev ;;
    d) run /private/var/x8d owners ;;
    e) run /private/var/x8e nobrowse,nosuid,nodev,owners ;;
    f) run /private/var/wbhomes nobrowse,nosuid,nodev,owners ;;
  esac
done
