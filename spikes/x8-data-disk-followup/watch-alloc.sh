#!/bin/sh
# watch-alloc.sh <bundle> <polls> <seconds>: print the data disk's
# allocated bytes on the host every <seconds>. Throwaway spike helper for
# I228.
set -eu
disk="$HOME/Library/Caches/wraithbox-spikes/$1/data.wbdisk"
i=0
while [ "$i" -lt "$2" ]; do
  blocks=$(stat -f %b "$disk")
  echo "$(date +%H:%M:%S) +$((i * $3)) s: $((blocks * 512)) B"
  i=$((i + 1))
  sleep "$3"
done
