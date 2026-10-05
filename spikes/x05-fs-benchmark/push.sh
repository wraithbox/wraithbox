#!/bin/sh
# X05-fs-benchmark spike: copy a host directory into the guest, as a tar
# stream over SSH. Nothing on the host reads the guest's disks.
# Usage: push.sh <scratch dir> <host parent dir> <name> <guest parent dir>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
t0=$(date +%s)
tar -C "$2" -cf - "$3" | "$here/gssh.sh" "$1" "mkdir -p '$4' && tar -C '$4' -xf -"
echo "pushed $2/$3 to $4 in $(($(date +%s) - t0)) s"
