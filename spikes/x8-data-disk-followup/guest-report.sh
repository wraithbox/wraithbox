#!/bin/sh
# guest-report.sh <uuid>: in the test guest, print what diskutil and
# mount report for the host-made data volume. Throwaway spike helper
# for I228.
set -u
uuid="$1"
echo "== sw_vers"; sw_vers
echo "== uptime"; uptime
echo "== mount (data volume)"; mount | grep -i -e wbhomes -e WraithBoxData || echo "not mounted"
echo "== diskutil info $uuid"
diskutil info "$uuid" | grep -E "Device Identifier|Part of Whole|Volume Name|Mount Point|File System Personality|Owners|Encrypted|FileVault|Solid State|Device Location|Protocol|Removable|Volume Used Space|Container Free Space"
container=$(diskutil info "$uuid" | awk -F': *' '/Part of Whole/ { print $2 }')
echo "== diskutil apfs list $container"
diskutil apfs list "$container"
