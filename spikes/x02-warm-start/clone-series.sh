#!/bin/sh
# X02-warm-start: N restores, each from a fresh APFS clone of <bundle>,
# so the state file is not in the host page cache (a new clone is a new
# file). Usage: clone-series.sh <x02 binary> <bundle> <runs> <out.jsonl>
set -u
x02=$1
src=$2
runs=$3
out=$4
cd "$HOME/Library/Caches/wraithbox-spikes/x02" || exit 1
i=1
while [ "$i" -le "$runs" ]; do
  rm -rf series
  cp -c -R "$src" series
  "$x02" restore series 2 >>"$out" 2>/dev/null
  i=$((i + 1))
  sleep 2
done
rm -rf series
